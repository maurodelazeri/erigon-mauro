// Copyright 2024 The Erigon Authors
// This file is part of Erigon.
//
// Erigon is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// Erigon is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with Erigon. If not, see <http://www.gnu.org/licenses/>.

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/holiman/uint256"
	"github.com/mattn/go-colorable"
	"github.com/redis/go-redis/v9"
	"github.com/rs/cors"

	libcommon "github.com/erigontech/erigon-lib/common"
	"github.com/erigontech/erigon-lib/kv"
	"github.com/erigontech/erigon-lib/kv/mdbx"
	"github.com/erigontech/erigon-lib/log/v3"
	"github.com/erigontech/erigon/core/rawdb"
	redisstate "github.com/erigontech/erigon/redis-state"
	"github.com/erigontech/erigon/rpc"
)

var (
	datadir          = flag.String("datadir", "", "Path to Erigon data directory")
	chaindata        = flag.String("chaindata", "", "Path to Erigon chaindata directory (if different from <datadir>/chaindata)")
	redisURL         = flag.String("redis-url", "redis://localhost:6379/0", "Redis connection URL")
	redisPassword    = flag.String("redis-password", "", "Redis password")
	httpAddr         = flag.String("http.addr", "localhost", "HTTP-RPC server listening interface")
	httpPort         = flag.String("http.port", "8545", "HTTP-RPC server listening port")
	httpAPI          = flag.String("http.api", "eth,debug,net,web3", "API's offered over the HTTP-RPC interface")
	httpCorsDomain   = flag.String("http.corsdomain", "", "Comma separated list of domains from which to accept cross origin requests (browser enforced)")
	httpVirtualHosts = flag.String("http.vhosts", "localhost", "Comma separated list of virtual hostnames from which to accept requests (server enforced). Accepts '*' wildcard.")
	wsEnabled        = flag.Bool("ws", false, "Enable the WS-RPC server")
	wsAddr           = flag.String("ws.addr", "localhost", "WS-RPC server listening interface")
	wsPort           = flag.String("ws.port", "8546", "WS-RPC server listening port")
	wsAPI            = flag.String("ws.api", "eth,debug,net,web3", "API's offered over the WS-RPC interface")
	wsOrigins        = flag.String("ws.origins", "", "Origins from which to accept websockets requests")
	dumpBlock        = flag.String("dump-block", "", "Dump state at specified block to Redis (specify 'latest' for latest block)")
	logLevelFlag     = flag.String("log.level", "info", "Log level (trace, debug, info, warn, error, crit)")
)

func main() {
	flag.Parse()

	// Configure logger
	logLevel := log.LvlInfo
	if *logLevelFlag != "" {
		var err error
		logLevel, err = log.LvlFromString(*logLevelFlag)
		if err != nil {
			fmt.Printf("Invalid log level: %s\n", *logLevelFlag)
			os.Exit(1)
		}
	}

	log.Root().SetHandler(log.LvlFilterHandler(logLevel, log.StreamHandler(colorable.NewColorableStdout(), log.TerminalFormat())))
	logger := log.New()

	// Connect to Redis
	logger.Info("Connecting to Redis", "url", *redisURL)

	opts, err := redis.ParseURL(*redisURL)
	if err != nil {
		logger.Error("Failed to parse Redis URL", "err", err)
		os.Exit(1)
	}

	if *redisPassword != "" {
		opts.Password = *redisPassword
	}

	redisClient := redis.NewClient(opts)
	ctx := context.Background()

	// Test Redis connection
	if err := redisClient.Ping(ctx).Err(); err != nil {
		logger.Error("Failed to connect to Redis", "err", err)
		os.Exit(1)
	}

	// If dump-block is specified, dump state and exit
	if *dumpBlock != "" {
		if err := dumpStateToRedis(ctx, logger, redisClient); err != nil {
			logger.Error("Failed to dump state", "err", err)
			os.Exit(1)
		}
		logger.Info("State dump completed")
		os.Exit(0)
	}

	// Start RPC server
	if err := startRPCServer(logger, redisClient); err != nil {
		logger.Error("Failed to start RPC server", "err", err)
		os.Exit(1)
	}

	// Wait for interrupt signal
	sigint := make(chan os.Signal, 1)
	signal.Notify(sigint, syscall.SIGINT, syscall.SIGTERM)
	<-sigint

	logger.Info("Shutting down")
}

func dumpStateToRedis(ctx context.Context, logger log.Logger, redisClient *redis.Client) error {
	if *datadir == "" {
		return errors.New("datadir must be specified")
	}

	chainDataPath := *chaindata
	if chainDataPath == "" {
		chainDataPath = filepath.Join(*datadir, "chaindata")
	}

	// Open Erigon database
	db, err := openDatabase(chainDataPath, logger)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer db.Close()

	dumper := redisstate.NewStateDumper(db, redisClient, logger)

	var blockNum uint64
	if *dumpBlock == "latest" {
		// Get the latest block number
		if err := db.View(context.Background(), func(tx kv.Tx) error {
			blockNumPtr := rawdb.ReadCurrentBlockNumber(tx)
			if blockNumPtr == nil {
				return fmt.Errorf("failed to get current block number")
			}
			blockNum = *blockNumPtr
			return nil
		}); err != nil {
			return fmt.Errorf("failed to get latest block number: %w", err)
		}
	} else {
		// Parse block number
		blockNum, err = strconv.ParseUint(*dumpBlock, 10, 64)
		if err != nil {
			return fmt.Errorf("invalid block number: %w", err)
		}
	}

	logger.Info("Dumping state", "blockNum", blockNum)
	if err := dumper.DumpState(blockNum); err != nil {
		return fmt.Errorf("failed to dump state: %w", err)
	}

	return nil
}

func openDatabase(path string, logger log.Logger) (kv.RoDB, error) {
	logger.Info("Opening database", "path", path)

	// Open database in read-only mode
	db := mdbx.New(kv.ChainDB, logger).
		Readonly(true).
		Path(path)

	mdbxDB, err := db.Open(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to open MDBX database: %w", err)
	}

	return mdbxDB, nil
}

func startRPCServer(logger log.Logger, redisClient *redis.Client) error {
	stateProvider := redisstate.NewRedisStateProvider(redisClient, logger)

	// Create API backend
	ethBackend := createEthAPI(stateProvider, logger)
	debugBackend := createDebugAPI(stateProvider, logger)

	// Create RPC server
	// Parameters: batchConcurrency, traceRequests, debugSingleRequest, disableStreaming, logger, rpcSlowLogThreshold
	srv := rpc.NewServer(16, false, false, false, logger, 5*time.Second)

	// Parse APIs to enable
	apiList := parseAPIList(*httpAPI)

	// Register APIs
	for _, api := range apiList {
		switch api {
		case "eth":
			if err := srv.RegisterName("eth", ethBackend); err != nil {
				return fmt.Errorf("failed to register eth API: %w", err)
			}
		case "debug":
			if err := srv.RegisterName("debug", debugBackend); err != nil {
				return fmt.Errorf("failed to register debug API: %w", err)
			}
		case "net":
			if err := srv.RegisterName("net", createNetAPI(logger)); err != nil {
				return fmt.Errorf("failed to register net API: %w", err)
			}
		case "web3":
			if err := srv.RegisterName("web3", createWeb3API(logger)); err != nil {
				return fmt.Errorf("failed to register web3 API: %w", err)
			}
		}
	}

	logger.Info("Enabled APIs", "apis", apiList)

	// Setup HTTP server
	if *httpAddr != "" {
		httpEndpoint := fmt.Sprintf("%s:%s", *httpAddr, *httpPort)

		// Parse CORS domains
		corsDomains := parseCORSDomains(*httpCorsDomain)

		// Parse virtual hosts
		vhosts := parseVirtualHosts(*httpVirtualHosts)

		// Create and start HTTP server
		httpServer := &http.Server{
			Addr:    httpEndpoint,
			Handler: newCorsHandler(srv, corsDomains, vhosts),
		}

		go func() {
			if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				logger.Error("HTTP server failed", "err", err)
			}
		}()

		logger.Info("HTTP endpoint opened", "url", fmt.Sprintf("http://%s", httpEndpoint))
	}

	// Setup WebSocket server if enabled
	if *wsEnabled {
		wsEndpoint := fmt.Sprintf("%s:%s", *wsAddr, *wsPort)
		wsOriginsList := parseCORSDomains(*wsOrigins)

		// Create WebSocket handler
		// Fix: Pass all required arguments to WebsocketHandler
		wsHandler := srv.WebsocketHandler(wsOriginsList, nil, false, logger)

		// Create and start WebSocket server
		wsServer := &http.Server{
			Addr:    wsEndpoint,
			Handler: wsHandler,
		}

		go func() {
			if err := wsServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				logger.Error("WebSocket server failed", "err", err)
			}
		}()

		logger.Info("WebSocket endpoint opened", "url", fmt.Sprintf("ws://%s", wsEndpoint))
	}

	return nil
}

// Helper functions to parse API settings
func parseAPIList(apiList string) []string {
	apis := strings.Split(apiList, ",")
	result := make([]string, 0, len(apis))

	for _, api := range apis {
		api = strings.TrimSpace(api)
		if api != "" {
			result = append(result, api)
		}
	}

	return result
}

func parseCORSDomains(corsFlag string) []string {
	if corsFlag == "" {
		return nil
	}
	domains := strings.Split(corsFlag, ",")
	for i := range domains {
		domains[i] = strings.TrimSpace(domains[i])
	}
	return domains
}

func parseVirtualHosts(hostsFlag string) []string {
	if hostsFlag == "" || hostsFlag == "*" {
		return []string{"*"}
	}
	hosts := strings.Split(hostsFlag, ",")
	for i := range hosts {
		hosts[i] = strings.TrimSpace(hosts[i])
	}
	return hosts
}

// CORS handler for HTTP server
func newCorsHandler(srv http.Handler, allowedOrigins []string, allowedVirtualHosts []string) http.Handler {
	// If CORS domains are set, create CORS middleware
	var corsHandler http.Handler
	if len(allowedOrigins) > 0 {
		corsHandler = cors.New(cors.Options{
			AllowedOrigins: allowedOrigins,
			AllowedMethods: []string{http.MethodPost, http.MethodGet},
			AllowedHeaders: []string{"*"},
			MaxAge:         600,
		}).Handler(srv)
	} else {
		corsHandler = srv
	}

	// Check virtual hosts if needed
	if len(allowedVirtualHosts) > 0 && !contains(allowedVirtualHosts, "*") {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			host := r.Host
			if host != "" {
				for _, allowedHost := range allowedVirtualHosts {
					if allowedHost == host {
						corsHandler.ServeHTTP(w, r)
						return
					}
				}
			}
			http.Error(w, "Invalid host specified", http.StatusForbidden)
		})
	}

	return corsHandler
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

// Create API implementations
func createEthAPI(stateProvider *redisstate.RedisStateProvider, logger log.Logger) interface{} {
	return &EthAPI{stateProvider: stateProvider, logger: logger}
}

func createDebugAPI(stateProvider *redisstate.RedisStateProvider, logger log.Logger) interface{} {
	return &DebugAPI{stateProvider: stateProvider, logger: logger}
}

func createNetAPI(logger log.Logger) interface{} {
	return &NetAPI{logger: logger}
}

func createWeb3API(logger log.Logger) interface{} {
	return &Web3API{logger: logger}
}

// API implementations
type EthAPI struct {
	stateProvider *redisstate.RedisStateProvider
	logger        log.Logger
}

// Example implementation of eth_getBalance
func (api *EthAPI) GetBalance(ctx context.Context, address libcommon.Address, blockNumber string) (*uint256.Int, error) {
	// Convert block number string to rpc.BlockNumber
	var blockNum rpc.BlockNumber
	if blockNumber == "latest" {
		blockNum = rpc.LatestBlockNumber
	} else if blockNumber == "pending" {
		blockNum = rpc.PendingBlockNumber
	} else if blockNumber == "earliest" {
		blockNum = rpc.EarliestBlockNumber
	} else {
		// Parse hex string
		if strings.HasPrefix(blockNumber, "0x") {
			num, err := strconv.ParseUint(blockNumber[2:], 16, 64)
			if err != nil {
				return nil, fmt.Errorf("invalid block number: %s", blockNumber)
			}
			blockNum = rpc.BlockNumber(num)
		} else {
			// Try parsing as decimal
			num, err := strconv.ParseUint(blockNumber, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("invalid block number: %s", blockNumber)
			}
			blockNum = rpc.BlockNumber(num)
		}
	}

	// Get balance
	balance, err := api.stateProvider.BalanceAt(ctx, address, blockNum)
	if err != nil {
		return nil, err
	}

	// Convert to uint256
	result, overflow := uint256.FromBig(balance)
	if overflow {
		return nil, fmt.Errorf("balance overflows uint256: %s", balance.String())
	}

	return result, nil
}

type DebugAPI struct {
	stateProvider *redisstate.RedisStateProvider
	logger        log.Logger
}

// TraceTransaction implements debug_traceTransaction
func (api *DebugAPI) TraceTransaction(ctx context.Context, txHash libcommon.Hash, config interface{}) (interface{}, error) {
	// Implementation would go here
	return nil, fmt.Errorf("not implemented")
}

type NetAPI struct {
	logger log.Logger
}

// Version returns the network identifier
func (api *NetAPI) Version() string {
	return "1" // Mainnet
}

type Web3API struct {
	logger log.Logger
}

// ClientVersion returns the client version
func (api *Web3API) ClientVersion() string {
	return "Redis/v0.1.0"
}

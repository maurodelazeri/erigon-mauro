# Redis State Integration Guide

This guide provides specific, concrete steps to integrate the Redis state solution with an Erigon node. The goal is to **keep the Erigon node working normally** while adding interceptors at key points to mirror state changes to Redis.

## Overview

The integration requires changes in just three specific locations in Erigon's codebase:

1. **State Writer Interception**: Wrap the state writer during block execution to mirror state changes
2. **Block Processing Interception**: Add Redis mirroring after a block is successfully processed
3. **Initial Setup**: Configure Redis connection and initializing components

## Step 1: Configure Redis Client in Node Setup

First, we need to add Redis configuration to the Erigon node. Open `/cmd/erigon/main.go` and add Redis-related flags and setup:

```go
// Add these imports
import (
    "github.com/redis/go-redis/v9"
    redisstate "github.com/erigontech/erigon/redis-state"
)

// Add these as command-line flags
var (
    // Existing flags
    
    // Redis flags for state mirroring
    redisURLFlag = flag.String("redis.url", "", "Redis URL for state mirroring (empty to disable)")
    redisPasswordFlag = flag.String("redis.password", "", "Redis password for state mirroring")
)

// Then in the main() function, add Redis client setup:
func main() {
    // Existing initialization code
    
    // Initialize Redis client if URL is provided
    var redisClient *redis.Client
    var blockProcessor *redisstate.BlockHeaderProcessor
    
    if *redisURLFlag != "" {
        opts, err := redis.ParseURL(*redisURLFlag)
        if err != nil {
            utils.Fatalf("Failed to parse Redis URL: %v", err)
        }
        
        if *redisPasswordFlag != "" {
            opts.Password = *redisPasswordFlag
        }
        
        redisClient = redis.NewClient(opts)
        ctx := context.Background()
        
        // Test Redis connection
        if err := redisClient.Ping(ctx).Err(); err != nil {
            utils.Fatalf("Failed to connect to Redis: %v", err)
        }
        
        log.Info("Connected to Redis for state mirroring", "url", *redisURLFlag)
        
        // Create a block processor for header and receipt processing
        blockProcessor = redisstate.NewBlockHeaderProcessor(redisClient, log.New())
    }
    
    // Continue with normal Erigon initialization, but pass redisClient to the relevant components
}
```

## Step 2: Intercept State Changes During Block Execution

The key point for state interception is in Erigon's block execution stage. Open `/core/state_processor.go` and locate the `ApplyTransaction` function. We need to wrap the state writer:

```go
// Find this function
func ApplyTransaction(config *chain.Config, bc ChainContext, author *libcommon.Address, gp *GasPool, ibs evmtypes.IntraBlockState, stateWriter state.StateWriter, header *types.Header, tx types.Transaction, usedGas *uint64, evm *vm.EVM, cfg vm.Config) (*types.Receipt, error) {
    // Modify the function to accept redisClient parameter
}

// In your main execution loop inside stages/execution.go, find where ApplyTransaction is called

// Inside this function
func executeBlock(/* existing params */, redisClient *redis.Client) error {
    
    // Find where the state writer is created, typically something like:
    stateWriter := state.NewStateWriter(batch, blockNum)
    
    // Wrap it with our interceptor
    var wrappedStateWriter state.StateWriter
    if redisClient != nil {
        wrappedStateWriter = redisstate.NewHistoricalStateInterceptor(stateWriter, redisClient, blockNum, logger)
    } else {
        wrappedStateWriter = stateWriter
    }
    
    // Use wrappedStateWriter instead of stateWriter for subsequent operations
    
    // When ApplyTransaction is called, use the wrapped writer
    receipt, err := ApplyTransaction(/* existing params with wrappedStateWriter */)
    
    // Continue with normal execution
}
```

## Step 3: Process Block Headers and Receipts

After a block is successfully executed, we need to store the block header and receipts in Redis. In `/eth/backend.go`, find the block insertion method:

```go
// Find a function like this
func (s *EthBackend) InsertBlock(block *types.Block, receipts types.Receipts) error {
    // Existing block processing code
    
    // After the block is successfully committed, mirror to Redis if enabled
    if redisClient != nil && blockProcessor != nil {
        if err := blockProcessor.ProcessBlockHeader(block.Header()); err != nil {
            log.Warn("Failed to mirror block header to Redis", "err", err, "block", block.NumberU64())
            // Don't return error - we continue even if Redis mirroring fails
        }
        
        if err := blockProcessor.ProcessBlockReceipts(block, receipts); err != nil {
            log.Warn("Failed to mirror block receipts to Redis", "err", err, "block", block.NumberU64())
            // Don't return error - we continue even if Redis mirroring fails
        }
    }
    
    // Continue with normal processing
    return nil
}
```

## Complete Integration Example

Here's a more complete example showing how to integrate at all key points. This example assumes you've added the Redis client setup from Step 1.

### Integration in Staged Sync

The primary integration point for Erigon is in the staged sync process, specifically in the execution stage:

```go
// In stages/stage_execute.go

// Add this import
import (
    redisstate "github.com/erigontech/erigon/redis-state"
)

// Modify ExecCfg to include Redis
type ExecCfg struct {
    // Existing fields
    redisClient *redis.Client
    // Other fields
}

// Modify ExecuteBlocksStage to use Redis
func ExecuteBlocksStage(/* existing params */, cfg *ExecCfg) error {
    // Existing code
    
    // Inside the execution loop for each block
    for blockNum := from; blockNum <= to; blockNum++ {
        // Find where the state writer is created
        stateWriter, err := state.NewStateWriter(batch, blockNum)
        if err != nil {
            return err
        }
        
        // Wrap with Redis interceptor if Redis is enabled
        var wrappedStateWriter state.StateWriter
        if cfg.redisClient != nil {
            wrappedStateWriter = redisstate.NewHistoricalStateInterceptor(stateWriter, cfg.redisClient, blockNum, logger)
        } else {
            wrappedStateWriter = stateWriter
        }
        
        // Use wrappedStateWriter in subsequent code
        
        // After successful block execution, process header and receipts
        if cfg.redisClient != nil {
            blockProcessor := redisstate.NewBlockHeaderProcessor(cfg.redisClient, logger)
            
            if err := blockProcessor.ProcessBlockHeader(header); err != nil {
                logger.Warn("Failed to mirror block header to Redis", "err", err, "block", blockNum)
                // Don't return error - continue even if Redis mirroring fails
            }
            
            if err := blockProcessor.ProcessBlockReceipts(block, receipts); err != nil {
                logger.Warn("Failed to mirror block receipts to Redis", "err", err, "block", blockNum)
                // Don't return error - continue even if Redis mirroring fails
            }
        }
    }
    
    // Rest of the function
}
```

### Integration in Transaction Processing

For real-time transaction mirroring, integrate in the transaction pool:

```go
// In txpool/tx_pool.go

// Modify add transaction method
func (pool *TxPool) addTx(tx *types.Transaction, local bool, redisClient *redis.Client) error {
    // Existing code
    
    // After the transaction is successfully added
    if redisClient != nil {
        blockWriter := redisstate.NewRedisBlockWriter(redisClient)
        
        // Store the transaction with its hash (not committed to a block yet)
        txHash := tx.Hash()
        txJson, _ := json.Marshal(tx)
        if err := blockWriter.WriteTransaction(txHash, 0, txJson); err != nil {
            // Log but continue
            pool.logger.Warn("Failed to mirror transaction to Redis", "err", err, "txHash", txHash.Hex())
        }
    }
    
    // Continue with normal processing
}
```

## Practical Integration Steps

To integrate Redis state mirroring into your Erigon node:

1. **Ensure Redis-State Module is Built**:
   ```bash
   go build -o ./build/bin/redis-state ./cmd/redis-state
   ```

2. **Add Redis Integration Code to Erigon**:
   - Add the code examples shown above to the appropriate files
   - Make sure to pass Redis client to all necessary components

3. **Update Erigon Command-Line Flags**:
   - Add Redis URL and password flags as shown
   - Document this in the Erigon help output

4. **Rebuild Erigon**:
   ```bash
   go build -o ./build/bin/erigon ./cmd/erigon
   ```

5. **Run Erigon with Redis Enabled**:
   ```bash
   ./build/bin/erigon --redis.url="redis://localhost:6379/0" [other flags]
   ```

## Fallback Handling

A critical aspect of this integration is graceful degradation. If Redis becomes unavailable, Erigon should continue to operate normally:

```go
// Example of proper error handling
func mirrorToRedis(redisClient *redis.Client, fn func() error) {
    if redisClient == nil {
        return // Do nothing if Redis is not configured
    }
    
    // Attempt the Redis operation
    err := fn()
    if err != nil {
        if errors.Is(err, redis.ErrClosed) || strings.Contains(err.Error(), "connection refused") {
            // Redis connection issue - log and continue
            log.Warn("Redis connection issue, continuing without state mirroring", "err", err)
        } else {
            // Other Redis error - log and continue
            log.Warn("Redis operation failed, continuing without state mirroring", "err", err)
        }
    }
}

// Use like this
mirrorToRedis(redisClient, func() error {
    return blockProcessor.ProcessBlockHeader(header)
})
```

## Verification

After integrating, you can verify it's working by:

1. Starting a Redis server
2. Running Erigon with Redis enabled
3. Letting it process some blocks
4. Using the Redis CLI to check if data is being stored:

```bash
redis-cli> KEYS *
redis-cli> ZRANGE account:0x1234567890abcdef 0 -1 WITHSCORES
```

## Summary

This integration approach:

1. Keeps Erigon working normally if Redis is unavailable
2. Captures all state changes in real-time
3. Has minimal impact on Erigon's performance
4. Requires changes in only a few, well-defined locations

By following these steps, you'll have a complete O(1) state mirroring solution that allows querying any historical state instantly via Redis, while maintaining full compatibility with the regular Erigon node functionality.
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

package redisstate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strconv"

	"github.com/holiman/uint256"
	"github.com/redis/go-redis/v9"

	"github.com/erigontech/erigon-lib/chain"
	libcommon "github.com/erigontech/erigon-lib/common"
	"github.com/erigontech/erigon-lib/log/v3"
	"github.com/erigontech/erigon-lib/types/accounts"
	"github.com/erigontech/erigon/core/state"
	"github.com/erigontech/erigon/core/tracing"
	"github.com/erigontech/erigon/core/types"
	"github.com/erigontech/erigon/core/vm/evmtypes"
	"github.com/erigontech/erigon/rpc"
)

// RedisStateProvider implements the state provider interface for Erigon's RPC API
type RedisStateProvider struct {
	client *redis.Client
	ctx    context.Context
	logger log.Logger
}

// NewRedisStateProvider creates a new instance of RedisStateProvider
func NewRedisStateProvider(client *redis.Client, logger log.Logger) *RedisStateProvider {
	return &RedisStateProvider{
		client: client,
		ctx:    context.Background(),
		logger: logger,
	}
}

// BlockByNumber returns a block by its number
func (p *RedisStateProvider) BlockByNumber(ctx context.Context, number rpc.BlockNumber) (*types.Block, error) {
	// Convert BlockNumber to uint64
	var blockNum uint64
	switch number {
	case rpc.LatestBlockNumber, rpc.PendingBlockNumber:
		// Get the latest block number from Redis
		result, err := p.client.Get(p.ctx, "currentBlock").Result()
		if err != nil {
			return nil, fmt.Errorf("failed to get latest block number: %w", err)
		}

		blockNum, err = strconv.ParseUint(result, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("failed to parse block number: %w", err)
		}
	case rpc.EarliestBlockNumber:
		blockNum = 0
	default:
		blockNum = uint64(number)
	}

	// Get block hash and header
	blockKey := fmt.Sprintf("block:%d", blockNum)
	headerBytes, err := p.client.HGet(p.ctx, blockKey, "header").Result()
	if err == redis.Nil {
		return nil, fmt.Errorf("block not found: %d", blockNum)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get block header: %w", err)
	}

	// Parse header
	var header types.Header
	if err := json.Unmarshal([]byte(headerBytes), &header); err != nil {
		return nil, fmt.Errorf("failed to unmarshal header: %w", err)
	}

	// For simplicity, we're returning a block with just the header
	// In a real implementation, you'd also fetch the transactions and receipts
	block := types.NewBlockWithHeader(&header)
	return block, nil
}

// BlockByHash returns a block by its hash
func (p *RedisStateProvider) BlockByHash(ctx context.Context, hash libcommon.Hash) (*types.Block, error) {
	// Convert hash to block number
	hashKey := fmt.Sprintf("blockHash:%s", hash.Hex())
	blockNumStr, err := p.client.Get(p.ctx, hashKey).Result()
	if err == redis.Nil {
		return nil, fmt.Errorf("block hash not found: %s", hash.Hex())
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get block number for hash: %w", err)
	}

	blockNum, err := strconv.ParseUint(blockNumStr, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("failed to parse block number: %w", err)
	}

	// Use BlockByNumber to get the block
	return p.BlockByNumber(ctx, rpc.BlockNumber(blockNum))
}

// StateAtBlock returns a state reader for a specific block
func (p *RedisStateProvider) StateAtBlock(ctx context.Context, block *types.Block) (evmtypes.IntraBlockState, error) {
	if block == nil {
		return nil, errors.New("block is nil")
	}

	// Create a reader for the block's state
	reader := NewPointInTimeRedisStateReader(p.client, block.NumberU64())
	state := NewRedisIntraBlockState(reader, block.NumberU64())

	return state, nil
}

// StateAtTransaction returns a state reader for a specific transaction
func (p *RedisStateProvider) StateAtTransaction(ctx context.Context, block *types.Block, txIndex int) (evmtypes.IntraBlockState, *types.Transaction, error) {
	if block == nil {
		return nil, nil, errors.New("block is nil")
	}

	transactions := block.Transactions()
	if txIndex < 0 || txIndex >= len(transactions) {
		return nil, nil, fmt.Errorf("transaction index out of range: %d", txIndex)
	}

	// Get the transaction from the block - it's already a pointer
	tx := transactions[txIndex]

	// Create a state reader for the block's state
	state, err := p.StateAtBlock(ctx, block)
	if err != nil {
		return nil, nil, err
	}

	return state, &tx, nil
}

// BalanceAt returns the balance of the given account at the given block
func (p *RedisStateProvider) BalanceAt(ctx context.Context, address libcommon.Address, blockNumber rpc.BlockNumber) (*big.Int, error) {
	// Convert BlockNumber to uint64
	var blockNum uint64
	switch blockNumber {
	case rpc.LatestBlockNumber, rpc.PendingBlockNumber:
		// Get the latest block number from Redis
		result, err := p.client.Get(p.ctx, "currentBlock").Result()
		if err != nil {
			return nil, fmt.Errorf("failed to get latest block number: %w", err)
		}

		blockNum, err = strconv.ParseUint(result, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("failed to parse block number: %w", err)
		}
	case rpc.EarliestBlockNumber:
		blockNum = 0
	default:
		blockNum = uint64(blockNumber)
	}

	// Use the Redis state reader to get the account data
	reader := NewPointInTimeRedisStateReader(p.client, blockNum)
	account, err := reader.ReadAccountData(address)
	if err != nil {
		return nil, fmt.Errorf("failed to read account data: %w", err)
	}

	if account == nil {
		return big.NewInt(0), nil
	}

	return account.Balance.ToBig(), nil
}

// PointInTimeRedisStateReader extends RedisStateReader to read state at a specific block
type PointInTimeRedisStateReader struct {
	RedisStateReader
	blockNum uint64
}

// NewPointInTimeRedisStateReader creates a new instance of PointInTimeRedisStateReader
func NewPointInTimeRedisStateReader(client *redis.Client, blockNum uint64) *PointInTimeRedisStateReader {
	return &PointInTimeRedisStateReader{
		RedisStateReader: RedisStateReader{
			client: client,
			ctx:    context.Background(),
		},
		blockNum: blockNum,
	}
}

// ReadAccountData reads account data from Redis at a specific block
func (r *PointInTimeRedisStateReader) ReadAccountData(address libcommon.Address) (*accounts.Account, error) {
	key := accountKey(address)

	// Get the most recent account data before or at the specified block
	result := r.client.ZRevRangeByScore(r.ctx, key, &redis.ZRangeBy{
		Min:    "0",
		Max:    fmt.Sprintf("%d", r.blockNum),
		Offset: 0,
		Count:  1,
	})

	if result.Err() != nil {
		return nil, result.Err()
	}

	values, err := result.Result()
	if err != nil {
		return nil, err
	}

	if len(values) == 0 {
		return nil, nil // Account doesn't exist at this block
	}

	var serialized SerializedAccount
	if err := json.Unmarshal([]byte(values[0]), &serialized); err != nil {
		return nil, err
	}

	balance, err := uint256.FromHex(serialized.Balance)
	if err != nil {
		return nil, err
	}

	return &accounts.Account{
		Nonce:       serialized.Nonce,
		Balance:     *balance,
		CodeHash:    serialized.CodeHash,
		Incarnation: serialized.Incarnation,
	}, nil
}

// ReadAccountStorage reads account storage from Redis at a specific block
func (r *PointInTimeRedisStateReader) ReadAccountStorage(address libcommon.Address, incarnation uint64, key *libcommon.Hash) ([]byte, error) {
	storageKeyStr := storageKey(address, key)

	// Get the most recent storage data before or at the specified block
	result := r.client.ZRevRangeByScore(r.ctx, storageKeyStr, &redis.ZRangeBy{
		Min:    "0",
		Max:    fmt.Sprintf("%d", r.blockNum),
		Offset: 0,
		Count:  1,
	})

	if result.Err() != nil {
		return nil, result.Err()
	}

	values, err := result.Result()
	if err != nil {
		return nil, err
	}

	if len(values) == 0 {
		return nil, nil // Storage doesn't exist at this block
	}

	return []byte(values[0]), nil
}

// accessList is a simple implementation of an access list for our RedisIntraBlockState
type accessList struct {
	addresses map[libcommon.Address]bool
	slots     map[libcommon.Address]map[libcommon.Hash]bool
}

func newAccessList() *accessList {
	return &accessList{
		addresses: make(map[libcommon.Address]bool),
		slots:     make(map[libcommon.Address]map[libcommon.Hash]bool),
	}
}

func (al *accessList) ContainsAddress(addr libcommon.Address) bool {
	return al.addresses[addr]
}

func (al *accessList) AddAddress(addr libcommon.Address) bool {
	if al.addresses[addr] {
		return false
	}
	al.addresses[addr] = true
	return true
}

func (al *accessList) AddSlot(addr libcommon.Address, slot libcommon.Hash) (bool, bool) {
	addrChange := al.AddAddress(addr)
	
	if al.slots[addr] == nil {
		al.slots[addr] = make(map[libcommon.Hash]bool)
	}
	
	if al.slots[addr][slot] {
		return addrChange, false
	}
	
	al.slots[addr][slot] = true
	return addrChange, true
}

// RedisIntraBlockState implements the evmtypes.IntraBlockState interface using Redis
type RedisIntraBlockState struct {
	stateReader state.StateReader
	blockNum    uint64
	hooks       *tracing.Hooks
	accessList  *accessList
	refund      uint64
}

// NewRedisIntraBlockState creates a new instance of RedisIntraBlockState
func NewRedisIntraBlockState(stateReader state.StateReader, blockNum uint64) *RedisIntraBlockState {
	return &RedisIntraBlockState{
		stateReader: stateReader,
		blockNum:    blockNum,
		accessList:  newAccessList(),
	}
}

// Implementation of required evmtypes.IntraBlockState methods

// CreateAccount creates a new account
func (s *RedisIntraBlockState) CreateAccount(addr libcommon.Address, contractCreation bool) error {
	return errors.New("not implemented: read-only state")
}

// SubBalance subtracts amount from account
func (s *RedisIntraBlockState) SubBalance(addr libcommon.Address, amount *uint256.Int, reason tracing.BalanceChangeReason) error {
	return errors.New("not implemented: read-only state")
}

// AddBalance adds amount to account
func (s *RedisIntraBlockState) AddBalance(addr libcommon.Address, amount *uint256.Int, reason tracing.BalanceChangeReason) error {
	return errors.New("not implemented: read-only state")
}

// GetBalance returns the balance of the given account
func (s *RedisIntraBlockState) GetBalance(addr libcommon.Address) (*uint256.Int, error) {
	account, err := s.stateReader.ReadAccountData(addr)
	if err != nil {
		return nil, err
	}
	if account == nil {
		return uint256.NewInt(0), nil
	}

	result := new(uint256.Int).Set(&account.Balance)
	return result, nil
}

// GetNonce returns the nonce of the given account
func (s *RedisIntraBlockState) GetNonce(addr libcommon.Address) (uint64, error) {
	account, err := s.stateReader.ReadAccountData(addr)
	if err != nil {
		return 0, err
	}
	if account == nil {
		return 0, nil
	}
	return account.Nonce, nil
}

// SetNonce sets the nonce of the account
func (s *RedisIntraBlockState) SetNonce(addr libcommon.Address, nonce uint64) error {
	return errors.New("not implemented: read-only state")
}

// GetCodeHash returns the code hash of the given account
func (s *RedisIntraBlockState) GetCodeHash(addr libcommon.Address) (libcommon.Hash, error) {
	account, err := s.stateReader.ReadAccountData(addr)
	if err != nil {
		return libcommon.Hash{}, err
	}
	if account == nil {
		return libcommon.Hash{}, nil
	}
	return account.CodeHash, nil
}

// GetCode returns the code of the given account
func (s *RedisIntraBlockState) GetCode(addr libcommon.Address) ([]byte, error) {
	account, err := s.stateReader.ReadAccountData(addr)
	if err != nil {
		return nil, err
	}
	if account == nil {
		return nil, nil
	}

	code, err := s.stateReader.ReadAccountCode(addr, account.Incarnation)
	if err != nil {
		return nil, err
	}
	return code, nil
}

// SetCode sets the code of the account
func (s *RedisIntraBlockState) SetCode(addr libcommon.Address, code []byte) error {
	return errors.New("not implemented: read-only state")
}

// GetCodeSize returns the code size of the given account
func (s *RedisIntraBlockState) GetCodeSize(addr libcommon.Address) (int, error) {
	code, err := s.GetCode(addr)
	if err != nil {
		return 0, err
	}
	return len(code), nil
}

// ResolveCodeHash returns code hash, potentially delegated
func (s *RedisIntraBlockState) ResolveCodeHash(addr libcommon.Address) (libcommon.Hash, error) {
	return s.GetCodeHash(addr)
}

// ResolveCode returns code, potentially delegated
func (s *RedisIntraBlockState) ResolveCode(addr libcommon.Address) ([]byte, error) {
	return s.GetCode(addr)
}

// GetDelegatedDesignation returns designated address
func (s *RedisIntraBlockState) GetDelegatedDesignation(addr libcommon.Address) (libcommon.Address, bool, error) {
	return libcommon.Address{}, false, nil
}

// AddRefund adds to the refund counter
func (s *RedisIntraBlockState) AddRefund(gas uint64) {
	s.refund += gas
}

// SubRefund subtracts from the refund counter
func (s *RedisIntraBlockState) SubRefund(gas uint64) {
	if gas > s.refund {
		s.refund = 0
	} else {
		s.refund -= gas
	}
}

// GetRefund returns the refund counter
func (s *RedisIntraBlockState) GetRefund() uint64 {
	return s.refund
}

// GetCommittedState gets the committed state
func (s *RedisIntraBlockState) GetCommittedState(addr libcommon.Address, key *libcommon.Hash, outValue *uint256.Int) error {
	return s.GetState(addr, key, outValue)
}

// GetState gets the state value
func (s *RedisIntraBlockState) GetState(addr libcommon.Address, slot *libcommon.Hash, outValue *uint256.Int) error {
	account, err := s.stateReader.ReadAccountData(addr)
	if err != nil {
		return err
	}
	if account == nil {
		outValue.Clear()
		return nil
	}

	value, err := s.stateReader.ReadAccountStorage(addr, account.Incarnation, slot)
	if err != nil {
		return err
	}

	if len(value) == 0 {
		outValue.Clear()
		return nil
	}

	outValue.SetBytes(value)
	return nil
}

// SetState sets the state value
func (s *RedisIntraBlockState) SetState(addr libcommon.Address, key *libcommon.Hash, value uint256.Int) error {
	return errors.New("not implemented: read-only state")
}

// GetTransientState gets the transient state
func (s *RedisIntraBlockState) GetTransientState(addr libcommon.Address, key libcommon.Hash) uint256.Int {
	var value uint256.Int
	return value
}

// SetTransientState sets the transient state
func (s *RedisIntraBlockState) SetTransientState(addr libcommon.Address, key libcommon.Hash, value uint256.Int) {
	// No-op in read-only mode
}

// Selfdestruct marks the contract for self-destruction
func (s *RedisIntraBlockState) Selfdestruct(addr libcommon.Address) (bool, error) {
	return false, errors.New("not implemented: read-only state")
}

// HasSelfdestructed reports whether the contract was selfdestructed
func (s *RedisIntraBlockState) HasSelfdestructed(addr libcommon.Address) (bool, error) {
	return false, nil
}

// Selfdestruct6780 marks the contract for self-destruction with 6780 rules
func (s *RedisIntraBlockState) Selfdestruct6780(addr libcommon.Address) error {
	return errors.New("not implemented: read-only state")
}

// Exist reports whether the given account exists
func (s *RedisIntraBlockState) Exist(addr libcommon.Address) (bool, error) {
	account, err := s.stateReader.ReadAccountData(addr)
	if err != nil {
		return false, err
	}
	return account != nil, nil
}

// Empty returns whether the given account is empty
func (s *RedisIntraBlockState) Empty(addr libcommon.Address) (bool, error) {
	account, err := s.stateReader.ReadAccountData(addr)
	if err != nil {
		return true, err
	}
	if account == nil {
		return true, nil
	}
	return account.Nonce == 0 && account.Balance.IsZero() && account.CodeHash == (libcommon.Hash{}), nil
}

// Prepare prepares the access list from rules and transactions
func (s *RedisIntraBlockState) Prepare(rules *chain.Rules, sender, coinbase libcommon.Address, dest *libcommon.Address,
	precompiles []libcommon.Address, txAccesses types.AccessList, authorities []libcommon.Address) error {

	// Convert the transaction access list to the internal one
	s.accessList = newAccessList()
	for _, access := range txAccesses {
		s.accessList.AddAddress(access.Address)
		for _, key := range access.StorageKeys {
			s.accessList.AddSlot(access.Address, key)
		}
	}

	// Add the sender, coinbase and precompiled addresses
	s.accessList.AddAddress(sender)
	s.accessList.AddAddress(coinbase)
	for _, addr := range precompiles {
		s.accessList.AddAddress(addr)
	}

	// Add destination if there is one
	if dest != nil {
		s.accessList.AddAddress(*dest)
	}

	// Add authorities if provided
	for _, authority := range authorities {
		s.accessList.AddAddress(authority)
	}

	return nil
}

// AddressInAccessList returns whether an address is in the access list
func (s *RedisIntraBlockState) AddressInAccessList(addr libcommon.Address) bool {
	if s.accessList == nil {
		return false
	}
	return s.accessList.ContainsAddress(addr)
}

// AddAddressToAccessList adds an address to the access list
func (s *RedisIntraBlockState) AddAddressToAccessList(addr libcommon.Address) bool {
	if s.accessList == nil {
		s.accessList = newAccessList()
	}
	return s.accessList.AddAddress(addr)
}

// AddSlotToAccessList adds a slot to the access list
func (s *RedisIntraBlockState) AddSlotToAccessList(addr libcommon.Address, slot libcommon.Hash) (bool, bool) {
	if s.accessList == nil {
		s.accessList = newAccessList()
	}
	return s.accessList.AddSlot(addr, slot)
}

// RevertToSnapshot reverts to a given snapshot
func (s *RedisIntraBlockState) RevertToSnapshot(id int) {
	// No-op in read-only mode
}

// Snapshot creates a new snapshot
func (s *RedisIntraBlockState) Snapshot() int {
	return 0 // Always return 0 in read-only mode
}

// AddLog adds a log entry
func (s *RedisIntraBlockState) AddLog(log *types.Log) {
	// No-op in read-only mode
}

// AddPreimage adds a preimage
func (s *RedisIntraBlockState) AddPreimage(hash libcommon.Hash, preimage []byte) {
	// No-op in read-only mode
}

// SetHooks sets the tracing hooks
func (s *RedisIntraBlockState) SetHooks(hooks *tracing.Hooks) {
	s.hooks = hooks
}

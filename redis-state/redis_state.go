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

	"github.com/holiman/uint256"
	"github.com/redis/go-redis/v9"

	libcommon "github.com/erigontech/erigon-lib/common"
	"github.com/erigontech/erigon-lib/types/accounts"
	"github.com/erigontech/erigon/core/state"
)

// RedisStateReader implements the state.StateReader interface using Redis as the backing store
type RedisStateReader struct {
	client *redis.Client
	ctx    context.Context
}

// RedisStateWriter implements the state.StateWriter interface using Redis as the backing store
type RedisStateWriter struct {
	client   *redis.Client
	ctx      context.Context
	blockNum uint64
	txNum    uint64
}

// RedisHistoricalWriter extends RedisStateWriter with WriteChangeSets and WriteHistory methods
type RedisHistoricalWriter struct {
	RedisStateWriter
}

// SerializedAccount is a serializable version of accounts.Account
type SerializedAccount struct {
	Nonce       uint64         `json:"nonce"`
	Balance     string         `json:"balance"` // Using string for uint256
	CodeHash    libcommon.Hash `json:"codeHash"`
	Incarnation uint64         `json:"incarnation"`
}

// NewRedisStateReader creates a new instance of RedisStateReader
func NewRedisStateReader(client *redis.Client) *RedisStateReader {
	return &RedisStateReader{
		client: client,
		ctx:    context.Background(),
	}
}

// NewRedisStateWriter creates a new instance of RedisStateWriter
func NewRedisStateWriter(client *redis.Client, blockNum uint64) *RedisStateWriter {
	return &RedisStateWriter{
		client:   client,
		ctx:      context.Background(),
		blockNum: blockNum,
	}
}

// NewRedisHistoricalWriter creates a new instance of RedisHistoricalWriter
func NewRedisHistoricalWriter(client *redis.Client, blockNum uint64) *RedisHistoricalWriter {
	return &RedisHistoricalWriter{
		RedisStateWriter: *NewRedisStateWriter(client, blockNum),
	}
}

// SetTxNum sets the current transaction number being processed
func (w *RedisStateWriter) SetTxNum(txNum uint64) {
	w.txNum = txNum
}

// GetTxNum gets the current transaction number being processed
func (w *RedisStateWriter) GetTxNum() uint64 {
	return w.txNum
}

// WriteChangeSets writes change sets to Redis
func (w *RedisHistoricalWriter) WriteChangeSets() error {
	// No-op for Redis implementation as changes are written immediately
	return nil
}

// WriteHistory writes history to Redis
func (w *RedisHistoricalWriter) WriteHistory() error {
	// No-op for Redis implementation as all changes are stored with block numbers
	return nil
}

// accountKey creates the Redis key for an account
func accountKey(address libcommon.Address) string {
	return fmt.Sprintf("account:%s", address.Hex())
}

// storageKey creates the Redis key for a storage slot
func storageKey(address libcommon.Address, key *libcommon.Hash) string {
	return fmt.Sprintf("storage:%s:%s", address.Hex(), key.Hex())
}

// codeKey creates the Redis key for contract code
func codeKey(codeHash libcommon.Hash) string {
	return fmt.Sprintf("code:%s", codeHash.Hex())
}

// ReadAccountData reads account data from Redis
func (r *RedisStateReader) ReadAccountData(address libcommon.Address) (*accounts.Account, error) {
	key := accountKey(address)

	// Get the most recent account data before or at current block
	result := r.client.ZRevRangeByScore(r.ctx, key, &redis.ZRangeBy{
		Min:    "0",
		Max:    "+inf",
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
		return nil, nil // Account doesn't exist
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

// ReadAccountDataForDebug reads account data from Redis for debugging
func (r *RedisStateReader) ReadAccountDataForDebug(address libcommon.Address) (*accounts.Account, error) {
	return r.ReadAccountData(address)
}

// ReadAccountStorage reads account storage from Redis
func (r *RedisStateReader) ReadAccountStorage(address libcommon.Address, incarnation uint64, key *libcommon.Hash) ([]byte, error) {
	storageKeyStr := storageKey(address, key)

	// Get the most recent storage data before or at current block
	result := r.client.ZRevRangeByScore(r.ctx, storageKeyStr, &redis.ZRangeBy{
		Min:    "0",
		Max:    "+inf",
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
		return nil, nil // Storage doesn't exist
	}

	return []byte(values[0]), nil
}

// ReadAccountCode reads account code from Redis
func (r *RedisStateReader) ReadAccountCode(address libcommon.Address, incarnation uint64) ([]byte, error) {
	// First get the account to find the code hash
	account, err := r.ReadAccountData(address)
	if err != nil {
		return nil, err
	}
	if account == nil {
		return nil, nil
	}

	if account.Incarnation != incarnation {
		return nil, nil
	}

	// Check if it's empty code
	if account.CodeHash == (libcommon.Hash{}) {
		return nil, nil
	}

	// Get the code using the code hash
	key := codeKey(account.CodeHash)
	result := r.client.Get(r.ctx, key)
	if result.Err() == redis.Nil {
		return nil, nil
	}
	if result.Err() != nil {
		return nil, result.Err()
	}

	return []byte(result.Val()), nil
}

// ReadAccountCodeSize reads account code size from Redis
func (r *RedisStateReader) ReadAccountCodeSize(address libcommon.Address, incarnation uint64) (int, error) {
	code, err := r.ReadAccountCode(address, incarnation)
	if err != nil {
		return 0, err
	}
	return len(code), nil
}

// ReadAccountIncarnation reads account incarnation from Redis
func (r *RedisStateReader) ReadAccountIncarnation(address libcommon.Address) (uint64, error) {
	account, err := r.ReadAccountData(address)
	if err != nil {
		return 0, err
	}
	if account == nil {
		return 0, nil
	}
	return account.Incarnation, nil
}

// UpdateAccountData updates account data in Redis
func (w *RedisStateWriter) UpdateAccountData(address libcommon.Address, original, account *accounts.Account) error {
	if account == nil {
		return errors.New("account cannot be nil")
	}

	key := accountKey(address)
	serialized := SerializedAccount{
		Nonce:       account.Nonce,
		Balance:     account.Balance.Hex(),
		CodeHash:    account.CodeHash,
		Incarnation: account.Incarnation,
	}

	data, err := json.Marshal(serialized)
	if err != nil {
		return err
	}

	// Store the account with the current block number as score
	_, err = w.client.ZAdd(w.ctx, key, redis.Z{
		Score:  float64(w.blockNum),
		Member: string(data),
	}).Result()

	return err
}

// UpdateAccountCode updates account code in Redis
func (w *RedisStateWriter) UpdateAccountCode(address libcommon.Address, incarnation uint64, codeHash libcommon.Hash, code []byte) error {
	// Store code by hash (immutable)
	key := codeKey(codeHash)
	_, err := w.client.Set(w.ctx, key, code, 0).Result()
	return err
}

// DeleteAccount deletes an account in Redis
func (w *RedisStateWriter) DeleteAccount(address libcommon.Address, original *accounts.Account) error {
	// For deletion, we store an empty account with current block number
	key := accountKey(address)
	serialized := SerializedAccount{
		Nonce:       0,
		Balance:     "0x0",
		CodeHash:    libcommon.Hash{},
		Incarnation: 0,
	}

	data, err := json.Marshal(serialized)
	if err != nil {
		return err
	}

	// Store the deleted account with the current block number as score
	_, err = w.client.ZAdd(w.ctx, key, redis.Z{
		Score:  float64(w.blockNum),
		Member: string(data),
	}).Result()

	return err
}

// WriteAccountStorage writes account storage to Redis
func (w *RedisStateWriter) WriteAccountStorage(address libcommon.Address, incarnation uint64, key *libcommon.Hash, original, value *uint256.Int) error {
	storageKeyStr := storageKey(address, key)

	// Convert value to bytes
	var valueBytes []byte
	if value != nil && !value.IsZero() {
		valueBytes = value.Bytes()
	} else {
		valueBytes = []byte{} // Empty value for zero or nil
	}

	// Store storage with the current block number as score
	_, err := w.client.ZAdd(w.ctx, storageKeyStr, redis.Z{
		Score:  float64(w.blockNum),
		Member: string(valueBytes),
	}).Result()

	return err
}

// CreateContract creates a new contract in Redis
func (w *RedisStateWriter) CreateContract(address libcommon.Address) error {
	// Creating a contract just means setting the incarnation
	// We get the current account first
	reader := NewRedisStateReader(w.client)
	account, err := reader.ReadAccountData(address)
	if err != nil {
		return err
	}

	if account == nil {
		// New account
		account = &accounts.Account{
			Nonce:       0,
			Balance:     *uint256.NewInt(0),
			CodeHash:    libcommon.Hash{},
			Incarnation: state.FirstContractIncarnation,
		}
	} else {
		// Existing account, increment incarnation
		account.Incarnation = state.FirstContractIncarnation
		if account.Incarnation > 0 {
			account.Incarnation++
		}
	}

	return w.UpdateAccountData(address, nil, account)
}

// RedisBlockWriter handles writing block-related data to Redis
type RedisBlockWriter struct {
	client *redis.Client
	ctx    context.Context
}

// NewRedisBlockWriter creates a new instance of RedisBlockWriter
func NewRedisBlockWriter(client *redis.Client) *RedisBlockWriter {
	return &RedisBlockWriter{
		client: client,
		ctx:    context.Background(),
	}
}

// WriteBlockHeader writes a block header to Redis
func (w *RedisBlockWriter) WriteBlockHeader(blockNum uint64, blockHash libcommon.Hash, header []byte) error {
	// Store block header
	blockKey := fmt.Sprintf("block:%d", blockNum)
	if err := w.client.HSet(w.ctx, blockKey, "header", header).Err(); err != nil {
		return err
	}

	// Store mapping from hash to number
	hashKey := fmt.Sprintf("blockHash:%s", blockHash.Hex())
	if err := w.client.Set(w.ctx, hashKey, blockNum, 0).Err(); err != nil {
		return err
	}

	// Update current block pointer
	if err := w.client.Set(w.ctx, "currentBlock", blockNum, 0).Err(); err != nil {
		return err
	}

	return nil
}

// WriteReceipt writes a transaction receipt to Redis
func (w *RedisBlockWriter) WriteReceipt(txHash libcommon.Hash, blockNum uint64, receipt []byte) error {
	key := fmt.Sprintf("receipt:%s", txHash.Hex())

	// Store receipt with block number as score
	_, err := w.client.ZAdd(w.ctx, key, redis.Z{
		Score:  float64(blockNum),
		Member: string(receipt),
	}).Result()

	return err
}

// WriteLog writes a log entry to Redis
func (w *RedisBlockWriter) WriteLog(blockNum uint64, logIndex uint, address libcommon.Address, topics []libcommon.Hash, data []byte) error {
	// Create a log key
	logKey := fmt.Sprintf("logs:%d:%d", blockNum, logIndex)

	// Store the log data
	if err := w.client.Set(w.ctx, logKey, data, 0).Err(); err != nil {
		return err
	}

	// Index by address
	addrKey := fmt.Sprintf("address:%s", address.Hex())
	if err := w.client.ZAdd(w.ctx, addrKey, redis.Z{
		Score:  float64(blockNum),
		Member: logKey,
	}).Err(); err != nil {
		return err
	}

	// Index by topics
	for i, topic := range topics {
		// Global topic index
		topicKey := fmt.Sprintf("topic:%s", topic.Hex())
		if err := w.client.ZAdd(w.ctx, topicKey, redis.Z{
			Score:  float64(blockNum),
			Member: logKey,
		}).Err(); err != nil {
			return err
		}

		// Position-specific topic index
		posTopicKey := fmt.Sprintf("topic%d:%s", i, topic.Hex())
		if err := w.client.ZAdd(w.ctx, posTopicKey, redis.Z{
			Score:  float64(blockNum),
			Member: logKey,
		}).Err(); err != nil {
			return err
		}
	}

	return nil
}

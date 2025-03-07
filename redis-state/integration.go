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
	"fmt"

	"github.com/holiman/uint256"
	"github.com/redis/go-redis/v9"

	libcommon "github.com/erigontech/erigon-lib/common"
	"github.com/erigontech/erigon-lib/log/v3"
	"github.com/erigontech/erigon-lib/types/accounts"
	"github.com/erigontech/erigon/core/state"
	"github.com/erigontech/erigon/core/types"
)

// StateInterceptor wraps another StateWriter to mirror operations to Redis
type StateInterceptor struct {
	inner       state.StateWriter
	redisWriter *RedisStateWriter
	blockWriter *RedisBlockWriter
	blockNum    uint64
	logger      log.Logger
}

// NewStateInterceptor creates a new StateInterceptor
func NewStateInterceptor(inner state.StateWriter, redisClient *redis.Client, blockNum uint64, logger log.Logger) state.StateWriter {
	return &StateInterceptor{
		inner:       inner,
		redisWriter: NewRedisStateWriter(redisClient, blockNum),
		blockWriter: NewRedisBlockWriter(redisClient),
		blockNum:    blockNum,
		logger:      logger,
	}
}

// NewHistoricalStateInterceptor creates a new StateInterceptor that also implements WriterWithChangeSets
func NewHistoricalStateInterceptor(inner state.WriterWithChangeSets, redisClient *redis.Client, blockNum uint64, logger log.Logger) state.WriterWithChangeSets {
	return &HistoricalStateInterceptor{
		StateInterceptor: StateInterceptor{
			inner:       inner,
			redisWriter: NewRedisStateWriter(redisClient, blockNum),
			blockWriter: NewRedisBlockWriter(redisClient),
			blockNum:    blockNum,
			logger:      logger,
		},
		innerHistorical: inner,
		redisHistorical: NewRedisHistoricalWriter(redisClient, blockNum),
	}
}

// UpdateAccountData updates an account in the state and also in Redis
func (i *StateInterceptor) UpdateAccountData(address libcommon.Address, original, account *accounts.Account) error {
	// First update in the main state
	if err := i.inner.UpdateAccountData(address, original, account); err != nil {
		return err
	}

	// Also update in Redis, but don't fail if Redis update fails
	if err := i.redisWriter.UpdateAccountData(address, original, account); err != nil {
		i.logger.Warn("Failed to update account in Redis", "address", address.Hex(), "err", err)
	}

	return nil
}

// UpdateAccountCode updates account code in the state and also in Redis
func (i *StateInterceptor) UpdateAccountCode(address libcommon.Address, incarnation uint64, codeHash libcommon.Hash, code []byte) error {
	// First update in the main state
	if err := i.inner.UpdateAccountCode(address, incarnation, codeHash, code); err != nil {
		return err
	}

	// Also update in Redis, but don't fail if Redis update fails
	if err := i.redisWriter.UpdateAccountCode(address, incarnation, codeHash, code); err != nil {
		i.logger.Warn("Failed to update account code in Redis", "address", address.Hex(), "err", err)
	}

	return nil
}

// DeleteAccount deletes an account in the state and also in Redis
func (i *StateInterceptor) DeleteAccount(address libcommon.Address, original *accounts.Account) error {
	// First delete in the main state
	if err := i.inner.DeleteAccount(address, original); err != nil {
		return err
	}

	// Also delete in Redis, but don't fail if Redis update fails
	if err := i.redisWriter.DeleteAccount(address, original); err != nil {
		i.logger.Warn("Failed to delete account in Redis", "address", address.Hex(), "err", err)
	}

	return nil
}

// WriteAccountStorage writes account storage in the state and also in Redis
func (i *StateInterceptor) WriteAccountStorage(address libcommon.Address, incarnation uint64, key *libcommon.Hash, original, value *uint256.Int) error {
	// First write in the main state
	if err := i.inner.WriteAccountStorage(address, incarnation, key, original, value); err != nil {
		return err
	}

	// Also write in Redis, but don't fail if Redis update fails
	if err := i.redisWriter.WriteAccountStorage(address, incarnation, key, original, value); err != nil {
		i.logger.Warn("Failed to write account storage in Redis", "address", address.Hex(), "err", err)
	}

	return nil
}

// CreateContract creates a contract in the state and also in Redis
func (i *StateInterceptor) CreateContract(address libcommon.Address) error {
	// First create in the main state
	if err := i.inner.CreateContract(address); err != nil {
		return err
	}

	// Also create in Redis, but don't fail if Redis update fails
	if err := i.redisWriter.CreateContract(address); err != nil {
		i.logger.Warn("Failed to create contract in Redis", "address", address.Hex(), "err", err)
	}

	return nil
}

// HistoricalStateInterceptor adds support for WriteChangeSets and WriteHistory
type HistoricalStateInterceptor struct {
	StateInterceptor
	innerHistorical state.WriterWithChangeSets
	redisHistorical *RedisHistoricalWriter
}

// WriteChangeSets writes change sets in the state and also in Redis
func (i *HistoricalStateInterceptor) WriteChangeSets() error {
	// First write in the main state
	if err := i.innerHistorical.WriteChangeSets(); err != nil {
		return err
	}

	// Also write in Redis, but don't fail if Redis update fails
	if err := i.redisHistorical.WriteChangeSets(); err != nil {
		i.logger.Warn("Failed to write change sets in Redis", "err", err)
	}

	return nil
}

// WriteHistory writes history in the state and also in Redis
func (i *HistoricalStateInterceptor) WriteHistory() error {
	// First write in the main state
	if err := i.innerHistorical.WriteHistory(); err != nil {
		return err
	}

	// Also write in Redis, but don't fail if Redis update fails
	if err := i.redisHistorical.WriteHistory(); err != nil {
		i.logger.Warn("Failed to write history in Redis", "err", err)
	}

	return nil
}

// SetTxNum implements HistoricalStateReader interface if the inner does
func (i *HistoricalStateInterceptor) SetTxNum(txNum uint64) {
	if writer, ok := i.inner.(state.HistoricalStateReader); ok {
		writer.SetTxNum(txNum)
	}
	i.redisWriter.SetTxNum(txNum)
}

// GetTxNum implements HistoricalStateReader interface if the inner does
func (i *HistoricalStateInterceptor) GetTxNum() uint64 {
	if reader, ok := i.inner.(state.HistoricalStateReader); ok {
		return reader.GetTxNum()
	}
	return i.redisWriter.GetTxNum()
}

// BlockHeaderProcessor is responsible for processing block headers and storing them in Redis
type BlockHeaderProcessor struct {
	redisClient *redis.Client
	ctx         context.Context
	logger      log.Logger
}

// NewBlockHeaderProcessor creates a new BlockHeaderProcessor
func NewBlockHeaderProcessor(redisClient *redis.Client, logger log.Logger) *BlockHeaderProcessor {
	return &BlockHeaderProcessor{
		redisClient: redisClient,
		ctx:         context.Background(),
		logger:      logger,
	}
}

// ProcessBlockHeader processes a block header and stores it in Redis
func (p *BlockHeaderProcessor) ProcessBlockHeader(header *types.Header) error {
	blockWriter := NewRedisBlockWriter(p.redisClient)
	blockNum := header.Number.Uint64()
	blockHash := header.Hash()

	// Marshal header
	headerBytes, err := json.Marshal(header)
	if err != nil {
		return fmt.Errorf("failed to marshal header: %w", err)
	}

	// Write header to Redis
	if err := blockWriter.WriteBlockHeader(blockNum, blockHash, headerBytes); err != nil {
		return fmt.Errorf("failed to write block header: %w", err)
	}

	return nil
}

// ProcessBlockReceipts processes block receipts and stores them in Redis
func (p *BlockHeaderProcessor) ProcessBlockReceipts(block *types.Block, receipts types.Receipts) error {
	blockWriter := NewRedisBlockWriter(p.redisClient)
	blockNum := block.NumberU64()

	// Process receipts
	for i, receipt := range receipts {
		txHash := block.Transactions()[i].Hash()

		// Marshal receipt
		receiptBytes, err := json.Marshal(receipt)
		if err != nil {
			return fmt.Errorf("failed to marshal receipt: %w", err)
		}

		// Write receipt to Redis
		if err := blockWriter.WriteReceipt(txHash, blockNum, receiptBytes); err != nil {
			return fmt.Errorf("failed to write receipt: %w", err)
		}

		// Process logs
		for j, log := range receipt.Logs {
			logBytes, err := json.Marshal(log)
			if err != nil {
				return fmt.Errorf("failed to marshal log: %w", err)
			}

			if err := blockWriter.WriteLog(blockNum, uint(j), log.Address, log.Topics, logBytes); err != nil {
				return fmt.Errorf("failed to write log: %w", err)
			}
		}
	}

	return nil
}

// HandleBlock processes a block and writes its data to Redis
func (p *BlockHeaderProcessor) HandleBlock(block *types.Block, receipts types.Receipts) error {
	// Process header
	if err := p.ProcessBlockHeader(block.Header()); err != nil {
		return err
	}

	// Process receipts
	if err := p.ProcessBlockReceipts(block, receipts); err != nil {
		return err
	}

	p.logger.Info("Block processed and written to Redis", "block", block.NumberU64(), "hash", block.Hash().Hex())
	return nil
}

// WriteTransaction stores a transaction in Redis (for pending transactions)
func (p *BlockHeaderProcessor) WriteTransaction(txHash libcommon.Hash, blockNum uint64, txData []byte) error {
	key := fmt.Sprintf("tx:%s", txHash.Hex())
	
	// Store tx with block number as score (0 for pending)
	_, err := p.redisClient.ZAdd(p.ctx, key, redis.Z{
		Score:  float64(blockNum),
		Member: string(txData),
	}).Result()
	
	return err
}

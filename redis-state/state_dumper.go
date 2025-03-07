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
	"encoding/binary"
	"encoding/json"
	"fmt"
	"time"

	"github.com/holiman/uint256"
	"github.com/redis/go-redis/v9"

	libcommon "github.com/erigontech/erigon-lib/common"
	"github.com/erigontech/erigon-lib/kv"
	"github.com/erigontech/erigon-lib/log/v3"
	"github.com/erigontech/erigon-lib/types/accounts"
	"github.com/erigontech/erigon/core/rawdb"
	"github.com/erigontech/erigon/core/types"
)

// StateDumper is responsible for extracting the state from an Erigon database and loading it into Redis
type StateDumper struct {
	db          kv.RoDB
	redisClient *redis.Client
	ctx         context.Context
	logger      log.Logger
}

// NewStateDumper creates a new instance of StateDumper
func NewStateDumper(db kv.RoDB, redisClient *redis.Client, logger log.Logger) *StateDumper {
	return &StateDumper{
		db:          db,
		redisClient: redisClient,
		ctx:         context.Background(),
		logger:      logger,
	}
}

// DumpState extracts the state at a specific block and loads it into Redis
func (d *StateDumper) DumpState(blockNum uint64) error {
	start := time.Now()
	d.logger.Info("Starting state dump", "blockNum", blockNum)

	// Get the block hash and header
	var blockHash libcommon.Hash
	var header *types.Header
	if err := d.db.View(context.Background(), func(tx kv.Tx) error {
		var err error
		hash, err := rawdb.ReadCanonicalHash(tx, blockNum)
		if err != nil {
			return err
		}
		blockHash = hash

		header = rawdb.ReadHeader(tx, hash, blockNum)
		if header == nil {
			return fmt.Errorf("header not found for block %d with hash %s", blockNum, hash.Hex())
		}
		return nil
	}); err != nil {
		return fmt.Errorf("failed to get block hash and header: %w", err)
	}

	d.logger.Info("Dumping block header", "hash", blockHash.Hex())

	// Initialize a block writer
	blockWriter := NewRedisBlockWriter(d.redisClient)

	// Write the block header
	headerBytes, err := json.Marshal(header)
	if err != nil {
		return fmt.Errorf("failed to marshal header: %w", err)
	}

	if err := blockWriter.WriteBlockHeader(blockNum, blockHash, headerBytes); err != nil {
		return fmt.Errorf("failed to write block header: %w", err)
	}

	// Dump accounts and storage
	if err := d.dumpAccounts(blockNum); err != nil {
		return err
	}

	// Dump receipts and logs
	if err := d.dumpReceipts(blockNum, blockHash); err != nil {
		return err
	}

	d.logger.Info("State dump completed", "blockNum", blockNum, "duration", time.Since(start))
	return nil
}

// dumpAccounts extracts all accounts and their storage from the Erigon database
func (d *StateDumper) dumpAccounts(blockNum uint64) error {
	d.logger.Info("Dumping accounts", "blockNum", blockNum)

	// Initialize state writer for this block
	stateWriter := NewRedisStateWriter(d.redisClient, blockNum)

	// We'll track progress with a simple counter
	const batchSize = 1000
	count := 0

	accountCount := 0
	storageCount := 0
	codeCount := 0

	// Process accounts
	if err := d.db.View(context.Background(), func(tx kv.Tx) error {
		c, err := tx.Cursor(kv.PlainState)
		if err != nil {
			return err
		}
		defer c.Close()

		for k, v, err := c.First(); k != nil; k, v, err = c.Next() {
			if err != nil {
				return err
			}

			// PlainState keys are: address + incarnation + storageKey
			// If length is 20, it's an account, otherwise it's storage
			if len(k) == 20 {
				// Account data
				var address libcommon.Address
				copy(address[:], k)

				var acc accounts.Account
				if err := acc.DecodeForStorage(v); err != nil {
					d.logger.Warn("Failed to decode account", "address", address.Hex(), "err", err)
					continue
				}

				if err := stateWriter.UpdateAccountData(address, nil, &acc); err != nil {
					return fmt.Errorf("failed to write account: %w", err)
				}
				accountCount++

				// Handle code if present
				if acc.CodeHash != (libcommon.Hash{}) {
					// Get code using cursor
					var code []byte
					codeCursor, err := tx.Cursor(kv.Code)
					if err != nil {
						d.logger.Warn("Failed to create code cursor", "err", err)
						continue
					}

					// FIX: SeekExact returns 3 values: key, value, error
					// We're only interested in the value (code) and error
					_, code, err = codeCursor.SeekExact(acc.CodeHash[:])
					codeCursor.Close()

					if err != nil {
						d.logger.Warn("Failed to read code", "codeHash", acc.CodeHash.Hex(), "err", err)
					} else if len(code) > 0 {
						if err := stateWriter.UpdateAccountCode(address, acc.Incarnation, acc.CodeHash, code); err != nil {
							return fmt.Errorf("failed to write code: %w", err)
						}
						codeCount++
					}
				}

				count++
				if count >= batchSize {
					count = 0
					d.logger.Info("Progress", "accounts", accountCount, "storage", storageCount, "code", codeCount)
				}
			} else if len(k) > 20 {
				// Storage data
				var address libcommon.Address
				copy(address[:], k[:20])

				incarnation := uint64(0)
				if len(k) >= 28 {
					incarnation = binary.BigEndian.Uint64(k[20:28])
				}

				var storageKey libcommon.Hash
				if len(k) >= 28+32 {
					copy(storageKey[:], k[28:28+32])
				}

				value := uint256.NewInt(0)
				if len(v) > 0 {
					value.SetBytes(v)
				}

				if err := stateWriter.WriteAccountStorage(address, incarnation, &storageKey, nil, value); err != nil {
					return fmt.Errorf("failed to write storage: %w", err)
				}
				storageCount++

				count++
				if count >= batchSize {
					count = 0
					d.logger.Info("Progress", "accounts", accountCount, "storage", storageCount, "code", codeCount)
				}
			}
		}

		return nil
	}); err != nil {
		return err
	}

	// Log final progress
	if count > 0 {
		d.logger.Info("Progress", "accounts", accountCount, "storage", storageCount, "code", codeCount)
	}

	d.logger.Info("Account dump completed", "accounts", accountCount, "storage", storageCount, "code", codeCount)
	return nil
}

// dumpReceipts extracts transaction receipts and logs for a specific block
func (d *StateDumper) dumpReceipts(blockNum uint64, blockHash libcommon.Hash) error {
	d.logger.Info("Dumping receipts and logs", "blockNum", blockNum)

	// Initialize block writer
	blockWriter := NewRedisBlockWriter(d.redisClient)

	// Get block and receipts
	var block *types.Block
	if err := d.db.View(context.Background(), func(tx kv.Tx) error {
		block = rawdb.ReadBlock(tx, blockHash, blockNum)
		return nil
	}); err != nil {
		return fmt.Errorf("failed to read block: %w", err)
	}

	if block == nil {
		return fmt.Errorf("block not found: %d", blockNum)
	}

	var receipts types.Receipts
	if err := d.db.View(context.Background(), func(tx kv.Tx) error {
		receipts = rawdb.ReadRawReceipts(tx, block.NumberU64())
		return nil
	}); err != nil {
		return fmt.Errorf("failed to read receipts: %w", err)
	}

	// Process receipts and logs
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

	d.logger.Info("Receipt dump completed", "txCount", len(receipts))
	return nil
}

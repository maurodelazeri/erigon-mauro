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
	"fmt"

	"github.com/redis/go-redis/v9"

	libcommon "github.com/erigontech/erigon-lib/common"
)

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
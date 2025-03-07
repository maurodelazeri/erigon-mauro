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

	"github.com/holiman/uint256"
	"github.com/redis/go-redis/v9"

	libcommon "github.com/erigontech/erigon-lib/common"
	"github.com/erigontech/erigon-lib/crypto"
	"github.com/erigontech/erigon-lib/log/v3"
	"github.com/erigontech/erigon-lib/types/accounts"
	"github.com/erigontech/erigon/turbo/shards"
)

// RedisAccumulator wraps the regular accumulator to also write state changes to Redis
type RedisAccumulator struct {
	*shards.Accumulator
	redisClient *redis.Client
	redisWriter *RedisStateWriter
	logger      log.Logger
	ctx         context.Context
}

// NewRedisAccumulator creates a new RedisAccumulator
func NewRedisAccumulator(accumulator *shards.Accumulator, redisClient *redis.Client, blockNum uint64, logger log.Logger) *RedisAccumulator {
	return &RedisAccumulator{
		Accumulator: accumulator,
		redisClient: redisClient,
		redisWriter: NewRedisStateWriter(redisClient, blockNum),
		logger:      logger,
		ctx:         context.Background(),
	}
}

// ChangeAccount overrides the accumulator's ChangeAccount method
func (ra *RedisAccumulator) ChangeAccount(address libcommon.Address, incarnation uint64, data []byte) {
	// Call original method
	ra.Accumulator.ChangeAccount(address, incarnation, data)

	// Mirror to Redis
	var acc accounts.Account
	if err := accounts.DeserialiseV3(&acc, data); err == nil {
		if err := ra.redisWriter.UpdateAccountData(address, nil, &acc); err != nil {
			ra.logger.Error("Failed to write account to Redis", "address", address, "err", err)
		}
	} else {
		ra.logger.Error("Failed to deserialize account", "address", address, "err", err)
	}
}

// ChangeStorage overrides the accumulator's ChangeStorage method
func (ra *RedisAccumulator) ChangeStorage(address libcommon.Address, incarnation uint64, location libcommon.Hash, data []byte) {
	// Call original method
	ra.Accumulator.ChangeStorage(address, incarnation, location, data)

	// Mirror to Redis
	value := uint256.NewInt(0)
	if len(data) > 0 {
		value.SetBytes(data)
	}

	if err := ra.redisWriter.WriteAccountStorage(address, incarnation, &location, nil, value); err != nil {
		ra.logger.Error("Failed to write storage to Redis", "address", address, "location", location, "err", err)
	}
}

// ChangeCode overrides the accumulator's ChangeCode method
func (ra *RedisAccumulator) ChangeCode(address libcommon.Address, incarnation uint64, code []byte) {
	// Call original method
	ra.Accumulator.ChangeCode(address, incarnation, code)

	// Calculate code hash
	codeHash := libcommon.BytesToHash(crypto.Keccak256(code))

	// Mirror to Redis
	if err := ra.redisWriter.UpdateAccountCode(address, incarnation, codeHash, code); err != nil {
		ra.logger.Error("Failed to write code to Redis", "address", address, "err", err)
	}
}

// DeleteAccount overrides the accumulator's DeleteAccount method
func (ra *RedisAccumulator) DeleteAccount(address libcommon.Address) {
	// Call original method
	ra.Accumulator.DeleteAccount(address)

	// Mirror to Redis - we need to handle original account param being nil
	if err := ra.redisWriter.DeleteAccount(address, nil); err != nil {
		ra.logger.Error("Failed to delete account in Redis", "address", address, "err", err)
	}
}

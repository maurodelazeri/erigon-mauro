# Redis State Integration Guide

This document provides detailed instructions on how to integrate the Redis state solution with a running Erigon node for continuous state mirroring.

## Overview

To achieve real-time state mirroring from Erigon to Redis, you need to:

1. Modify the Erigon state processing pipeline to use the provided interceptors
2. Configure Redis connection settings
3. Set up the appropriate callback hooks for block processing

## Integration Steps

### 1. Add Required Imports

In your Erigon node's main package, add the following imports:

```go
import (
    "github.com/redis/go-redis/v9"
    
    "github.com/erigontech/erigon-lib/log/v3"
    redisstate "github.com/erigontech/erigon/redis-state"
)
```

### 2. Set Up Redis Client

Create a Redis client during node initialization:

```go
func setupRedisClient(redisURL, redisPassword string, logger log.Logger) (*redis.Client, error) {
    opts, err := redis.ParseURL(redisURL)
    if err != nil {
        return nil, fmt.Errorf("failed to parse Redis URL: %w", err)
    }
    
    if redisPassword != "" {
        opts.Password = redisPassword
    }
    
    client := redis.NewClient(opts)
    ctx := context.Background()
    
    // Test connection
    if err := client.Ping(ctx).Err(); err != nil {
        return nil, fmt.Errorf("failed to connect to Redis: %w", err)
    }
    
    logger.Info("Connected to Redis", "url", redisURL)
    return client, nil
}
```

### 3. Create State Interceptors

Modify your Erigon node's state processing to use the Redis state interceptors:

```go
// In your Erigon node's init or main function
redisClient, err := setupRedisClient("redis://localhost:6379/0", "", logger)
if err != nil {
    logger.Error("Failed to set up Redis client", "err", err)
    // Handle error or continue without Redis integration
} else {
    // Set up the block header processor
    blockProcessor := redisstate.NewBlockHeaderProcessor(redisClient, logger)
    
    // Hook this into your Erigon node's block processing callbacks
    // This varies depending on your Erigon node implementation
}
```

### 4. Integrate with State Stage

The key integration point is in the state execution stage of Erigon's staged sync. Find where the `state.WriterWithChangeSets` is created and wrap it with our interceptor:

```go
// Example modification to state stage execution
func modifyStateStage(stage *stages.StageState, s *StageState, tx kv.RwTx, blockNum uint64, redisClient *redis.Client, logger log.Logger) error {
    // Original code to create state writer
    stateWriter, err := state.NewStateWriter(tx, blockNum)
    if err != nil {
        return err
    }
    
    // Wrap with Redis interceptor
    redisStateWriter := redisstate.NewHistoricalStateInterceptor(stateWriter, redisClient, blockNum, logger)
    
    // Use redisStateWriter instead of stateWriter in the rest of the function
    // ...
    
    return nil
}
```

### 5. Process Block Headers and Receipts

You need to integrate with the block processing flow to ensure block headers and receipts are also mirrored to Redis:

```go
// Example function to process a new block
func processBlock(block *types.Block, receipts types.Receipts, blockProcessor *redisstate.BlockHeaderProcessor) error {
    // Process the block header
    if err := blockProcessor.ProcessBlockHeader(block.Header()); err != nil {
        return fmt.Errorf("failed to process block header: %w", err)
    }
    
    // Process receipts
    if err := blockProcessor.ProcessBlockReceipts(block, receipts); err != nil {
        return fmt.Errorf("failed to process receipts: %w", err)
    }
    
    return nil
}
```

### 6. Modify Transaction Processing

For real-time transaction tracking, add the Redis integration to the transaction processing path:

```go
// Example function for processing transactions
func processTx(tx *types.Transaction, blockNum uint64, txIndex uint, redisClient *redis.Client) error {
    // Create a block writer for this transaction
    blockWriter := redisstate.NewRedisBlockWriter(redisClient)
    
    // Additional processing logic here...
    
    return nil
}
```

## Integration with Erigon Execution Stages

Here's a more detailed example of how to integrate with Erigon's staged sync architecture:

### ExecutionStage Integration

```go
func NewExecutionStage(
    db kv.RwDB,
    config *params.ChainConfig,
    engine consensus.Engine,
    vmConfig *vm.Config,
    redisClient *redis.Client,
    logger log.Logger,
    /* other params */
) *StageState {
    execCfg := &ExecutionConfig{
        db:            db,
        engine:        engine,
        chainConfig:   config,
        vmConfig:      vmConfig,
        /* other fields */
        redisClient:   redisClient,
        logger:        logger,
    }
    
    return &StageState{
        ID:          stages.Execution,
        ExecutionFn: executeBlockWithRedis,
        Config:      execCfg,
    }
}

func executeBlockWithRedis(stage *stages.StageState, s *StageState, tx kv.RwTx) error {
    // Cast config
    execCfg := s.Config.(*ExecutionConfig)
    
    // Start block execution
    if err := tx.Update(context.Background(), func(tx kv.RwTx) error {
        // Get block number and hash
        blockNum := stage.BlockNumber
        
        // Create state writer with Redis integration
        stateWriter, err := state.NewStateWriter(tx, blockNum)
        if err != nil {
            return err
        }
        
        // Wrap with Redis interceptor
        redisStateWriter := redisstate.NewHistoricalStateInterceptor(
            stateWriter,
            execCfg.redisClient,
            blockNum,
            execCfg.logger,
        )
        
        // Create state reader
        stateReader, err := state.NewStateReader(tx)
        if err != nil {
            return err
        }
        
        // Execute block with Redis-enhanced state writer
        // Rest of block execution logic...
        
        return nil
    }); err != nil {
        return err
    }
    
    return nil
}
```

### Block Processing Integration

```go
func processBlockWithRedis(
    block *types.Block,
    receipts types.Receipts,
    redisClient *redis.Client,
    logger log.Logger,
) error {
    // Create block header processor
    blockProcessor := redisstate.NewBlockHeaderProcessor(redisClient, logger)
    
    // Process block header
    if err := blockProcessor.ProcessBlockHeader(block.Header()); err != nil {
        return fmt.Errorf("failed to process block header: %w", err)
    }
    
    // Process receipts
    if err := blockProcessor.ProcessBlockReceipts(block, receipts); err != nil {
        return fmt.Errorf("failed to process receipts: %w", err)
    }
    
    return nil
}
```

## Advanced Integration

### Custom State Reader Integration

If you have custom state readers in your Erigon implementation, you can integrate them with Redis:

```go
// Example custom state reader with Redis fallback
type HybridStateReader struct {
    erigonReader state.StateReader
    redisReader  *redisstate.RedisStateReader
}

func NewHybridStateReader(erigonTx kv.Tx, redisClient *redis.Client) (*HybridStateReader, error) {
    erigonReader, err := state.NewStateReader(erigonTx)
    if err != nil {
        return nil, err
    }
    
    redisReader := redisstate.NewRedisStateReader(redisClient)
    
    return &HybridStateReader{
        erigonReader: erigonReader,
        redisReader:  redisReader,
    }, nil
}

// Implement StateReader interface methods with fallback logic
// ...
```

### Error Handling and Recovery

Add robust error handling for Redis connectivity issues:

```go
func withRedisErrorHandling(
    fn func() error,
    logger log.Logger,
    operation string,
) error {
    err := fn()
    if err != nil {
        if errors.Is(err, redis.ErrClosed) || strings.Contains(err.Error(), "connection refused") {
            logger.Warn("Redis connection issue during operation, continuing without Redis",
                "operation", operation,
                "err", err,
            )
            // Continue without Redis
            return nil
        }
        // For other errors, return them
        return err
    }
    return nil
}

// Usage example
err = withRedisErrorHandling(
    func() error {
        return blockProcessor.ProcessBlockHeader(header)
    },
    logger,
    "process_block_header",
)
```

## Performance Considerations

### Batch Operations

For better performance, use batch operations when interacting with Redis:

```go
// Example batching for receipt processing
func batchProcessReceipts(
    block *types.Block,
    receipts types.Receipts,
    redisClient *redis.Client,
) error {
    pipe := redisClient.Pipeline()
    blockNum := block.NumberU64()
    
    // Batch operations
    for i, receipt := range receipts {
        txHash := block.Transactions()[i].Hash()
        receiptBytes, err := json.Marshal(receipt)
        if err != nil {
            return err
        }
        
        // Add to pipeline
        pipe.ZAdd(context.Background(), fmt.Sprintf("receipt:%s", txHash.Hex()), 
            redis.Z{
                Score:  float64(blockNum),
                Member: string(receiptBytes),
            },
        )
    }
    
    // Execute pipeline
    _, err := pipe.Exec(context.Background())
    return err
}
```

### Selective Mirroring

For very large deployments, consider selectively mirroring only certain accounts or state elements:

```go
func shouldMirrorAccount(address libcommon.Address, config *MirrorConfig) bool {
    // Example: Only mirror specific contract addresses
    for _, addr := range config.TrackedContracts {
        if address == addr {
            return true
        }
    }
    
    // Example: Always mirror accounts with large balances
    account, err := erigonStateReader.ReadAccountData(address)
    if err == nil && account != nil {
        // Check if balance exceeds threshold
        if account.Balance.Cmp(config.BalanceThreshold) >= 0 {
            return true
        }
    }
    
    return false
}
```

## Monitoring and Maintenance

### Health Checks

Add health check functions to monitor the Redis integration:

```go
func checkRedisHealth(client *redis.Client, logger log.Logger) bool {
    ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
    defer cancel()
    
    err := client.Ping(ctx).Err()
    if err != nil {
        logger.Error("Redis health check failed", "err", err)
        return false
    }
    
    return true
}
```

### Metrics Collection

Add metrics for monitoring Redis operations:

```go
var (
    redisOperationCount = metrics.NewCounterVec("redis_operations", "Number of Redis operations", []string{"operation"})
    redisLatency = metrics.NewHistogramVec("redis_latency", "Latency of Redis operations", []string{"operation"})
    redisErrors = metrics.NewCounterVec("redis_errors", "Number of Redis errors", []string{"operation"})
)

func trackRedisOperation(operation string, fn func() error) error {
    start := time.Now()
    redisOperationCount.WithLabelValues(operation).Inc()
    
    err := fn()
    
    latency := time.Since(start).Seconds()
    redisLatency.WithLabelValues(operation).Observe(latency)
    
    if err != nil {
        redisErrors.WithLabelValues(operation).Inc()
    }
    
    return err
}

// Usage example
err = trackRedisOperation("write_account", func() error {
    return redisWriter.UpdateAccountData(address, originalAccount, newAccount)
})
```

## Recovery and Resynchronization

If the Redis integration experiences issues and falls behind, you may need to resynchronize it:

```go
func resyncRedisState(
    db kv.RoDB,
    redisClient *redis.Client,
    fromBlock uint64,
    toBlock uint64,
    logger log.Logger,
) error {
    dumper := redisstate.NewStateDumper(db, redisClient, logger)
    
    // Dump state for each block in the range
    for blockNum := fromBlock; blockNum <= toBlock; blockNum++ {
        logger.Info("Resyncing block to Redis", "blockNum", blockNum)
        
        if err := dumper.DumpState(blockNum); err != nil {
            return fmt.Errorf("failed to dump state at block %d: %w", blockNum, err)
        }
    }
    
    return nil
}
```

## Configuration Example

Here's a complete example configuration structure for the Redis integration:

```go
type RedisConfig struct {
    // Connection details
    URL         string
    Password    string
    
    // Feature toggles
    EnableStateSync     bool
    EnableHeaderSync    bool
    EnableReceiptSync   bool
    EnableLogSync       bool
    
    // Performance tuning
    BatchSize           int
    SyncInterval        time.Duration
    MaxRetries          int
    RetryBackoff        time.Duration
    
    // Advanced features
    SelectiveSync       bool
    TrackedAddresses    []libcommon.Address
    TrackedStorageKeys  map[libcommon.Address][]libcommon.Hash
}

func NewDefaultRedisConfig() *RedisConfig {
    return &RedisConfig{
        URL:               "redis://localhost:6379/0",
        EnableStateSync:   true,
        EnableHeaderSync:  true,
        EnableReceiptSync: true,
        EnableLogSync:     true,
        BatchSize:         100,
        SyncInterval:      time.Second,
        MaxRetries:        3,
        RetryBackoff:      time.Second * 2,
        SelectiveSync:     false,
    }
}
```

## Conclusion

By following these integration steps, you can create a real-time state mirroring system from Erigon to Redis. This enables O(1) access to any historical state while maintaining the performance and reliability of the core Erigon node.

For further questions or assistance, please refer to the main README or open an issue in the Erigon GitHub repository.
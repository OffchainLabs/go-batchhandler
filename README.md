## Go-batchHander
Note: It's still under development (only supports arb1 blob submission tx now), not ready for public use.

### Import nitro
Clone this repo:
```bash
git clone https://github.com/Jason-W123/go-batchhandler.git
cd go-batchhandler
```

Clone submodules:
```bash
git submodule update --init --recursive --force
```

Enter nitro:
```bash
cd nitro
```

Build necessary libs:
```bash
make contracts
make build-node-deps
```

### Build

```bash
go build -o batchtool src/*
```

(Note: If you get this error `link: github.com/fjl/memsize: invalid reference to runtime.stopTheWorld` when build the code, please refer to [this pr](https://github.com/OffchainLabs/go-ethereum/pull/363/files) to fix.)

### Simple example

```bash
./batchtool decodebatch --child-chain-id 42161 --parent-chain-node-url {Arb1_Enpoint} --parent-chain-submission-tx-hash {Parent_Chain_Submission_Tx-Hash} --blob-client.beacon-url {Blob_Enpoint}
```

### Infomation you need know
Since this util is stateless, so it can't retrieve every transaction types including: `redeem transaction from retryables` and `batchSpendingReport transaction`.

`redeem transaction from retryables` will show up only if the auto-redeem success, if it not, the tx will not be recorded on blockchain. So to check the tx can be executed successfully or not, we need to get the state of the blockchain to verify it.

`batchSpendingReport transaction` is a delayed transaction type which sent from parent chain when a new batch is added. This delayed tx can report how much gas the batch spent. But as this is a delayed tx, so it will be added into sequencer a few batches later, because of this, not only we need the batch info which add this delayed to sequencer inbox but also we need the batch info which sent this delayed tx, so only 1 batch submission receipt is not enough here.

But those tx can be retrieved by some walkaround ways and will be supported in this repo later.

### Execution Options:

For `decodebatch`:
```
Usage of batchHandler:
      --blob-client.authorization string          Value to send with the HTTP Authorization: header for Beacon REST requests, must include both scheme and scheme parameters
      --blob-client.beacon-url string             Beacon Chain RPC URL to use for fetching blobs (normally on port 3500)
      --blob-client.blob-directory string         Full path of the directory to save fetched blobs
      --blob-client.secondary-beacon-url string   Backup beacon Chain RPC URL to use for fetching blobs (normally on port 3500) when unable to fetch from primary
      --child-chain-id uint                       Child chain id
      --parent-chain-node-url string              URL for parent chain node
      --parent-chain-submission-tx-hash string    The batch submission transaction hash
```

For `retrieveFromDAS`
    Not supported yet.

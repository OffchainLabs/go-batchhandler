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

### Simple example

```bash
./main decodebatch --child-chain-id 42161 --parent-chain-node-url {Arb1_Enpoint} --parent-chain-submission-tx-hash {Parent_Chain_Submission_Tx-Hash} --blob-client.beacon-url {Blob_Enpoint}
```
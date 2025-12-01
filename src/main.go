package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	flag "github.com/spf13/pflag"

	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/offchainlabs/nitro/arbnode"
	"github.com/offchainlabs/nitro/cmd/chaininfo"
	"github.com/offchainlabs/nitro/cmd/util/confighelpers"
	"github.com/offchainlabs/nitro/daprovider"
	"github.com/offchainlabs/nitro/solgen/go/bridgegen"
	"github.com/offchainlabs/nitro/util/headerreader"
)

type BatchHandlerType struct {
	ParentChainNodeURL    string                        `koanf:"parent-chain-node-url"`
	BatchSubmissionTxHash string                        `koanf:"parent-chain-submission-tx-hash"`
	ChildChainId          uint64                        `koanf:"child-chain-id"`
	BlobClient            headerreader.BlobClientConfig `koanf:"blob-client"`
	ChainInfoFile         string                        `koanf:"chain-info-file"`
}

type DasHandlerType struct {
	ParentChainNodeURL    string                        `koanf:"parent-chain-node-url"`
	BatchSubmissionTxHash string                        `koanf:"parent-chain-submission-tx-hash"`
	ChildChainId          uint64                        `koanf:"child-chain-id"`
	BlobClient            headerreader.BlobClientConfig `koanf:"blob-client"`
	ChainInfoFile         string                        `koanf:"chain-info-file"`
}

const defaultChainInfoFile = "../nitro/cmd/chaininfo/arbitrum_chain_info.json"

func main() {
	args := os.Args
	if len(args) < 2 {
		panic("Usage: batchtool [decodebatch|retrieveFromDAS] ...")
	}
	ctx := context.Background()

	var err error
	switch strings.ToLower(args[1]) {
	case "decodebatch":
		err = startBatchHandler(ctx, args[2:])
	case "retrieveFromDAS":
		err = startDASHandler(args[2:])

	default:
		panic(fmt.Sprintf("Unknown tool '%s' specified, valid tools are 'decodebatch', 'retrieveFromDAS'", args[1]))
	}
	if err != nil {
		panic(err)
	}
}

func parseBatchHandlerType(args []string) (*BatchHandlerType, error) {
	f := flag.NewFlagSet("batchHandler", flag.ContinueOnError)
	f.String("parent-chain-node-url", "", "URL for parent chain node")
	f.String("parent-chain-submission-tx-hash", "", "The batch submission transaction hash")
	f.Uint64("child-chain-id", 0, "Child chain id")
	f.String("chain-info-file", "", "Chain info file")
	headerreader.BlobClientAddOptions("blob-client", f)

	k, err := confighelpers.BeginCommonParse(f, args)
	if err != nil {
		println("error 1")
		return nil, err
	}

	var config BatchHandlerType
	if err := confighelpers.EndCommonParse(k, &config); err != nil {
		println("error 2")
		return nil, err
	}
	return &config, nil
}

func parseDasHandlerType(args []string) (*BatchHandlerType, error) {
	f := flag.NewFlagSet("batchHandler", flag.ContinueOnError)
	f.String("parent-chain-node-url", "", "URL for parent chain node")
	f.String("parent-chain-submission-tx-hash", "", "The batch submission transaction hash")
	f.Uint64("child-chain-id", 0, "Child chain id")
	f.String("chain-info-file", "", "Chain info file")

	k, err := confighelpers.BeginCommonParse(f, args)
	if err != nil {
		return nil, err
	}

	var config BatchHandlerType
	if err := confighelpers.EndCommonParse(k, &config); err != nil {
		return nil, err
	}
	return &config, nil
}

func startBatchHandler(ctx context.Context, args []string) error {
	config, err := parseBatchHandlerType(args)
	if err != nil {
		return err
	}
	// add user's chain info file to the chain info files
	chainInfoFiles := []string{defaultChainInfoFile}
	if config.ChainInfoFile != "" {
		chainInfoFiles = append(chainInfoFiles, config.ChainInfoFile)
	}

	chainConfig, err := chaininfo.GetRollupAddressesConfig(config.ChildChainId, "", chainInfoFiles, "")
	if err != nil {
		return err
	}
	var parentChainClient *ethclient.Client
	parentChainClient, err = ethclient.DialContext(ctx, config.ParentChainNodeURL)

	submissionTxReceipt, err := parentChainClient.TransactionReceipt(ctx, common.HexToHash(config.BatchSubmissionTxHash))

	if err != nil {
		return fmt.Errorf("failed to get transaction receipt: %w", err)
	}

	seqFilter, err := bridgegen.NewSequencerInboxFilterer(chainConfig.SequencerInbox, parentChainClient)

	if err != nil {
		return err
	}

	batchMap := make(map[uint64]*arbnode.SequencerInboxBatch)

	// We should use this way directly instead, however this needs to modify some codes in nitro source code
	// batch, err := getBatchFromSubmissionTx(submissionTxReceipt, seqFilter)
	// Instead we will use the following stupid way to get the batch instead

	// Because the function we need to extract batch from tx receipt is not implemented or non-accessible in nitro (some main function is private)
	// , we will use a stupid way to get the batch in this tool now. (Todo, add `getBatchFromSubmissionTx` function to nitro source code)
	seqInbox, err := arbnode.NewSequencerInbox(parentChainClient, chainConfig.SequencerInbox, 0)

	if err != nil {
		return err
	}

	// We get all batches in the block of the submission tx
	batches, err := seqInbox.LookupBatchesInRange(ctx, submissionTxReceipt.BlockNumber, submissionTxReceipt.BlockNumber)

	if err != nil {
		return err
	}

	// We get the target batch number of this submission tx
	targetBatchNum, err := getBatchSeqNumFromSubmission(submissionTxReceipt, seqFilter)

	if err != nil {
		return err
	}

	var batch *arbnode.SequencerInboxBatch

	// Compare all batches in the block with the target batch number and get the batch we need
	for _, subBatch := range batches {
		// keep all batches we got as it may help when we calculate batchSpendingReport tx hash
		batchMap[subBatch.SequenceNumber] = subBatch
		if subBatch.SequenceNumber == targetBatchNum {
			batch = subBatch
		}
	}

	if batch == nil {
		return ErrBatchNotFound
	}

	backend := &MultiplexerBackend{
		batchSeqNum:     targetBatchNum,
		batches:         batchMap,
		delayedMessages: nil,
		ctx:             ctx,
		client:          parentChainClient,
	}

	// We define a function to get batch data by seq num

	batchFetcher := func(batchNum uint64) ([]byte, error) {
		batchData, err := backend.GetBatchDataByNum(batchNum)
		if err != nil {
			if err == ErrUnknownBatch {
				return nil, nil
			} else {
				return nil, err
			}
		}
		return batchData, nil
	}

	// Now we need to get last batch's afterBatchDelayedCount, then we can get how many delayed msg in current batch by
	// delayed msg in current batch = current batch's afterBatchDelayedCount - last batch's afterBatchDelayedCount

	lastBatchDelayedCount, err := getAfterDelayedBySeqNum(int64(batch.SequenceNumber)-1, seqFilter)

	err = setDelayedToBackendByIndexRange(ctx, parentChainClient, chainConfig.SequencerInbox, chainConfig.Bridge, int64(lastBatchDelayedCount), int64(batch.AfterDelayedCount)-1, backend)
	if err != nil {
		return fmt.Errorf("failed to get delayed msg: %w", err)
	}

	// Get the batches related to target batch's `PostingReportBatch` tx
	err = getPostingReportBatchAndfillin(ctx, parentChainClient, seqInbox, backend, batchFetcher)

	if err != nil {
		return err
	}

	// initialize blob client
	blobClient, err := headerreader.NewBlobClient(config.BlobClient, parentChainClient)
	blobClient.Initialize(ctx)
	if err != nil {
		fmt.Println("failed to initialize blob client", "err", err)
		return err
	}

	dapReaders := daprovider.NewReaderRegistry()

	// We now only support blob submssion tx
	dapReaders.SetupBlobReader(daprovider.NewReaderForBlobReader(blobClient))

	// Get the bytes of main batch we are querying
	bytes, batchBlockHash, err := backend.PeekSequencerInbox()

	if err != nil {
		return err
	}

	// Get sequencer message from this batch
	parsedSequencerMsg, err := ParseSequencerMessage(ctx, backend.batchSeqNum, batchBlockHash, bytes, dapReaders, daprovider.KeysetPanicIfInvalid)

	if err != nil {
		return err
	}

	txes, err := getTxHash(parsedSequencerMsg, lastBatchDelayedCount, backend)
	if err != nil {
		fmt.Println("failed to get tx hash")
		return err
	}
	for i := 0; i < len(txes); i++ {
		fmt.Println(txes[i].Hash().Hex())
	}
	fmt.Println("Found tx numbder: ", len(txes))

	return nil
}

func startDASHandler(args []string) error {
	_, err := parseDasHandlerType(args)
	if err != nil {
		return err
	}

	fmt.Printf("retrieveFromDAS is not supported now")
	return nil
}

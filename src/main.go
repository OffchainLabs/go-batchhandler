package main

import (
	"context"
	"fmt"
	"math/big"
	"os"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	flag "github.com/spf13/pflag"

	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/offchainlabs/nitro/arbnode"
	"github.com/offchainlabs/nitro/arbstate/daprovider"
	"github.com/offchainlabs/nitro/cmd/util/confighelpers"
	"github.com/offchainlabs/nitro/util/headerreader"
)

type BatchHandlerType struct {
	ParentChainNodeURL    string                        `koanf:"parent-chain-node-url"`
	BatchSubmissionTxHash string                        `koanf:"parent-chain-submission-tx-hash"`
	ChildChainId          uint64                        `koanf:"child-chain-id"`
	BlobClient            headerreader.BlobClientConfig `koanf:"blob-client"`
}

type DasHandlerType struct {
	ParentChainNodeURL    string                        `koanf:"parent-chain-node-url"`
	BatchSubmissionTxHash string                        `koanf:"parent-chain-submission-tx-hash"`
	ChildChainId          uint64                        `koanf:"child-chain-id"`
	BlobClient            headerreader.BlobClientConfig `koanf:"blob-client"`
}

func main() {
	args := os.Args
	if len(args) < 2 {
		panic("Usage: batchtool [decodebatch|retrieveFromDAS|generatehash|dumpkeyset] ...")
	}
	ctx := context.Background()
	var err error
	switch strings.ToLower(args[1]) {
	case "decodebatch":
		err = startBatchHandler(ctx, args[2:])
	case "retrieveFromDAS":
		err = startDASHandler(args[2:])

	default:
		panic(fmt.Sprintf("Unknown tool '%s' specified, valid tools are 'client', 'keygen', 'generatehash'", args[1]))
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
		println("error 3")
		return err
	}

	var parentChainClient *ethclient.Client
	parentChainClient, err = ethclient.DialContext(context.TODO(), config.ParentChainNodeURL)

	if err != nil {
		return err
	}

	seqInbox, err := arbnode.NewSequencerInbox(parentChainClient, common.HexToAddress("0x1c479675ad559dc151f6ec7ed3fbf8cee79582b6"), int64(7262738))

	if err != nil {
		return err
	}

	batches, err := seqInbox.LookupBatchesInRange(ctx, big.NewInt(21384795), big.NewInt(21384797))
	// 21384796
	mybatch := batches[0]

	fmt.Println(*mybatch)

	// seqNum := mybatch.SequenceNumber

	// TODO: error handling
	// getAfterDelayedBySeqNum(ctx, parentChainClient, seqNum, common.HexToAddress("0x1c479675ad559dc151f6ec7ed3fbf8cee79582b6"), seqInbox)
	// seqInbox.

	// backend := &arbnode.multiplexerBackend{
	// 	batchSeqNum:           0,
	// }

	backend := &MultiplexerBackend{
		batchSeqNum:    batches[0].SequenceNumber,
		batch:          batches[0],
		delayedMessage: nil,
		ctx:            ctx,
		client:         parentChainClient,
	}

	blobClient, err := headerreader.NewBlobClient(config.BlobClient, parentChainClient)
	blobClient.Initialize(ctx)
	if err != nil {
		fmt.Println("failed to initialize blob client", "err", err)
		return err
	}

	var dapReaders []daprovider.Reader

	dapReaders = append(dapReaders, daprovider.NewReaderForBlobReader(blobClient))

	bytes, batchBlockHash, err := backend.PeekSequencerInbox()

	if err != nil {
		return err
	}

	parsedSequencerMsg, err := ParseSequencerMessage(ctx, backend.batchSeqNum, batchBlockHash, bytes, dapReaders, daprovider.KeysetPanicIfInvalid)

	if err != nil {
		return err
	}

	fmt.Println(parsedSequencerMsg)
	// multiplexer := arbstate.NewInboxMultiplexer(backend, 0, nil, daprovider.KeysetValidate)

	txes, err := getTxHash(parsedSequencerMsg)
	if err != nil {
		fmt.Println("failed to get tx hash")
		return err
	}
	for i := 0; i < len(txes); i++ {
		fmt.Println(txes[i].Hash().Hex())
	}
	fmt.Println("Find tx numbder: ", len(txes))
	// // multiplexer.
	// msgdata, err := multiplexer.Pop(context.TODO())

	// if err != nil {
	// 	if err == ErrEmptyDelayedMsg {
	// 		fmt.Println("empty inbox")
	// 	} else {
	// 		fmt.Println("error", err)
	// 	}
	// 	return err
	// }

	// fmt.Printf("Starting batch handler with config: %+v\n", config)
	return nil
}

func startDASHandler(args []string) error {
	config, err := parseDasHandlerType(args)
	if err != nil {
		return err
	}

	fmt.Printf("Starting DAS handler with config: %+v\n", config)
	return nil
}

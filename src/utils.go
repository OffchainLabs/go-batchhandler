package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/big"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/offchainlabs/nitro/arbcompress"
	"github.com/offchainlabs/nitro/arbnode"
	"github.com/offchainlabs/nitro/arbos"
	"github.com/offchainlabs/nitro/arbos/arbosState"
	"github.com/offchainlabs/nitro/arbos/arbostypes"
	"github.com/offchainlabs/nitro/arbos/l1pricing"
	"github.com/offchainlabs/nitro/arbstate"
	"github.com/offchainlabs/nitro/arbstate/daprovider"
	"github.com/offchainlabs/nitro/solgen/go/bridgegen"
	"github.com/offchainlabs/nitro/zeroheavy"
)

var ErrEmptyDelayedMsg = errors.New("output won't fit in maxsize")
var ErrUnknownDelayedMsg = errors.New("reading unknown delayed message")
var ErrOverwritingDelayedMsg = errors.New("overwriting delayed message")
var ErrOverwritingSeqMsg = errors.New("overwriting sequencer message")
var ErrUnknownBatch = errors.New("reading unknown sequencer batch")
var ErrSubmissionTx = errors.New("Not Correct batch submssion tx")
var ErrBatchNotFound = errors.New("Batch not found")

const maxZeroheavyDecompressedLen = 101*arbstate.MaxDecompressedLen/100 + 64

type sequencerMessage struct {
	minTimestamp         uint64
	maxTimestamp         uint64
	minL1Block           uint64
	maxL1Block           uint64
	afterDelayedMessages uint64
	segments             [][]byte
}

// var sequencerBridgeABI

// Reuse code in nitro/arbstate/inbox.go
func ParseSequencerMessage(ctx context.Context, batchNum uint64, batchBlockHash common.Hash, data []byte, dapReaders []daprovider.Reader, keysetValidationMode daprovider.KeysetValidationMode) (*sequencerMessage, error) {
	if len(data) < 40 {
		return nil, errors.New("sequencer message missing L1 header")
	}
	parsedMsg := &sequencerMessage{
		minTimestamp:         binary.BigEndian.Uint64(data[:8]),
		maxTimestamp:         binary.BigEndian.Uint64(data[8:16]),
		minL1Block:           binary.BigEndian.Uint64(data[16:24]),
		maxL1Block:           binary.BigEndian.Uint64(data[24:32]),
		afterDelayedMessages: binary.BigEndian.Uint64(data[32:40]),
		segments:             [][]byte{},
	}
	payload := data[40:]

	// Stage 0: Check if our node is out of date and we don't understand this batch type
	// If the parent chain sequencer inbox smart contract authenticated this batch,
	// an unknown header byte must mean that this node is out of date,
	// because the smart contract understands the header byte and this node doesn't.
	if len(payload) > 0 && daprovider.IsL1AuthenticatedMessageHeaderByte(payload[0]) && !daprovider.IsKnownHeaderByte(payload[0]) {
		return nil, fmt.Errorf("%w: batch has unsupported authenticated header byte 0x%02x", arbosState.ErrFatalNodeOutOfDate, payload[0])
	}

	// Stage 1: Extract the payload from any data availability header.
	// It's important that multiple DAS strategies can't both be invoked in the same batch,
	// as these headers are validated by the sequencer inbox and not other DASs.
	// We try to extract payload from the first occuring valid DA reader in the dapReaders list
	if len(payload) > 0 {
		foundDA := false
		var err error
		for _, dapReader := range dapReaders {
			if dapReader != nil && dapReader.IsValidHeaderByte(payload[0]) {
				payload, err = dapReader.RecoverPayloadFromBatch(ctx, batchNum, batchBlockHash, data, nil, keysetValidationMode != daprovider.KeysetDontValidate)
				if err != nil {
					// Matches the way keyset validation was done inside DAS readers i.e logging the error
					//  But other daproviders might just want to return the error
					if errors.Is(err, daprovider.ErrSeqMsgValidation) && daprovider.IsDASMessageHeaderByte(payload[0]) {
						logLevel := log.Error
						if keysetValidationMode == daprovider.KeysetPanicIfInvalid {
							logLevel = log.Crit
						}
						logLevel(err.Error())
					} else {
						return nil, err
					}
				}
				if payload == nil {
					return parsedMsg, nil
				}
				foundDA = true
				break
			}
		}

		if !foundDA {
			if daprovider.IsDASMessageHeaderByte(payload[0]) {
				log.Error("No DAS Reader configured, but sequencer message found with DAS header")
			} else if daprovider.IsBlobHashesHeaderByte(payload[0]) {
				return nil, daprovider.ErrNoBlobReader
			}
		}
	}

	// At this point, `payload` has not been validated by the sequencer inbox at all.
	// It's not safe to trust any part of the payload from this point onwards.

	// Stage 2: If enabled, decode the zero heavy payload (saves gas based on calldata charging).
	if len(payload) > 0 && daprovider.IsZeroheavyEncodedHeaderByte(payload[0]) {
		pl, err := io.ReadAll(io.LimitReader(zeroheavy.NewZeroheavyDecoder(bytes.NewReader(payload[1:])), int64(maxZeroheavyDecompressedLen)))
		if err != nil {
			log.Warn("error reading from zeroheavy decoder", err.Error())
			return parsedMsg, nil
		}
		payload = pl
	}

	// Stage 3: Decompress the brotli payload and fill the parsedMsg.segments list.
	if len(payload) > 0 && daprovider.IsBrotliMessageHeaderByte(payload[0]) {
		decompressed, err := arbcompress.Decompress(payload[1:], arbstate.MaxDecompressedLen)
		if err == nil {
			reader := bytes.NewReader(decompressed)
			stream := rlp.NewStream(reader, uint64(arbstate.MaxDecompressedLen))
			for {
				var segment []byte
				err := stream.Decode(&segment)
				if err != nil {
					if !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
						log.Warn("error parsing sequencer message segment", "err", err.Error())
					}
					break
				}
				if len(parsedMsg.segments) >= arbstate.MaxSegmentsPerSequencerMessage {
					log.Warn("too many segments in sequence batch")
					break
				}
				parsedMsg.segments = append(parsedMsg.segments, segment)
			}
		} else {
			log.Warn("sequencer msg decompression failed", "err", err)
		}
	} else {
		length := len(payload)
		if length == 0 {
			log.Warn("empty sequencer message")
		} else {
			log.Warn("unknown sequencer message format", "length", length, "firstByte", payload[0])
		}

	}

	return parsedMsg, nil
}

func getTxHash(parsedSequencerMsg *sequencerMessage, delayedStart uint64, backend *MultiplexerBackend) (txes types.Transactions, err error) {
	txHashes := make(types.Transactions, 0)
	delayedPos := delayedStart
	segments := parsedSequencerMsg.segments
	for i := 0; i < len(segments); i++ {
		segment := segments[i]
		kind := segment[0]
		segment = segment[1:]
		if kind == arbstate.BatchSegmentKindL2Message || kind == arbstate.BatchSegmentKindL2MessageBrotli {

			if kind == arbstate.BatchSegmentKindL2MessageBrotli {
				decompressed, err := arbcompress.Decompress(segment, arbostypes.MaxL2MessageSize)
				if err != nil {
					log.Info("dropping compressed message", "err", err, "delayedMsg")
					return nil, err
				}
				segment = decompressed
			}

			// We don't need blockNumber and timestamp to calculate tx hash
			msg := &arbostypes.L1IncomingMessage{
				Header: &arbostypes.L1IncomingMessageHeader{
					Kind:        arbostypes.L1MessageType_L2Message,
					Poster:      l1pricing.BatchPosterAddress,
					BlockNumber: 0,
					Timestamp:   0,
					RequestId:   nil,
					L1BaseFee:   big.NewInt(0),
				},
				L2msg: segment,
			}

			txHash, err := arbos.ParseL2Transactions(msg, big.NewInt(42161))
			if err != nil {
				return nil, err
			}
			txHashes = append(txHashes, txHash...)
		} else if kind == arbstate.BatchSegmentKindDelayedMessages {
			delayed, realErr := backend.ReadDelayedInbox(delayedPos)
			if realErr != nil {
				return nil, realErr
			}
			if delayed == nil {
				delayedPos += 1
				// Todo
				continue
			}
			txHash, err := arbos.ParseL2Transactions(delayed, big.NewInt(42161))
			if err != nil {
				delayedPos += 1
				// Todo: if tx is BatchPostingReportMessage, use current way will be failed
				continue
			}
			txHashes = append(txHashes, txHash...)

			delayedPos += 1

		} else if kind == arbstate.BatchSegmentKindAdvanceTimestamp || kind == arbstate.BatchSegmentKindAdvanceL1BlockNumber {
			continue
		} else {
			log.Error("bad sequencer message segment kind", "segmentNum", i, "kind", kind)
			return nil, nil
		}
	}
	return txHashes, nil
}

// This function is not usable because rawLog is private field in arbnode.SequencerInboxBatch
func getBatchFromSubmissionTx(tx *types.Receipt, seqFilter *bridgegen.SequencerInboxFilterer) (*arbnode.SequencerInboxBatch, error) {
	logs := tx.Logs

	sequencerBridgeABI, err := bridgegen.SequencerInboxMetaData.GetAbi()
	if err != nil {
		panic(err)
	}
	batchDeliveredID := sequencerBridgeABI.Events["SequencerBatchDelivered"].ID

	for _, log := range logs {
		// We just need SequencerBatchDelivered log here
		if log.Topics[0] != batchDeliveredID {
			continue
		}
		parsedLog, err := seqFilter.ParseSequencerBatchDelivered(*log)
		if err != nil {
			return nil, err
		}
		if !parsedLog.BatchSequenceNumber.IsUint64() {
			return nil, errors.New("sequencer inbox event has non-uint64 sequence number")
		}
		if !parsedLog.AfterDelayedMessagesRead.IsUint64() {
			return nil, errors.New("sequencer inbox event has non-uint64 delayed messages read")
		}

		seqNum := parsedLog.BatchSequenceNumber.Uint64()

		batch := &arbnode.SequencerInboxBatch{
			BlockHash:              log.BlockHash,
			ParentChainBlockNumber: log.BlockNumber,
			SequenceNumber:         seqNum,
			BeforeInboxAcc:         parsedLog.BeforeAcc,
			AfterInboxAcc:          parsedLog.AfterAcc,
			AfterDelayedAcc:        parsedLog.DelayedAcc,
			AfterDelayedCount:      parsedLog.AfterDelayedMessagesRead.Uint64(),
			// rawLog:                 *log,
			TimeBounds: parsedLog.TimeBounds,
			// dataLocation:           batchDataLocation(parsedLog.DataLocation),
			// bridgeAddress:          log.Address,
		}
		return batch, nil
	}
	return nil, ErrSubmissionTx
}

// traverse whole delayed messages recorded in backend and get which block the first BatchPostingReportMessage starts and
// last end, then use seqInbox.LookupBatchesInRange to get all those batches' info. Finally, fill in those batches to delayed.
func getPostingReportBatchAndfillin(ctx context.Context, client *ethclient.Client, seqInbox *arbnode.SequencerInbox, backend *MultiplexerBackend, batchFetcher arbostypes.FallibleBatchFetcher) error {
	delayedMessages := backend.delayedMessages
	var startBlock uint64 = ^uint64(0) // max uint64 value
	var endBlock uint64
	for _, delayedMsg := range delayedMessages {
		if delayedMsg == nil {
			continue
		}
		if delayedMsg.Header.Kind != arbostypes.L1MessageType_BatchPostingReport {
			continue
		}
		if delayedMsg.Header.BlockNumber < startBlock {
			startBlock = delayedMsg.Header.BlockNumber
		}
		if delayedMsg.Header.BlockNumber > endBlock {
			endBlock = delayedMsg.Header.BlockNumber
		}
	}
	// Don't find any BatchPostingReportMessage
	if startBlock == ^uint64(0) {
		return nil
	}
	batches, err := seqInbox.LookupBatchesInRange(ctx, big.NewInt(int64(startBlock)), big.NewInt(int64(endBlock)))
	if err != nil {
		return err
	}
	// Store the batch to backend so it can be queryed by batchFetcher later
	for _, batch := range batches {
		backend.SetInboxMessage(batch.SequenceNumber, batch)
	}
	// Fill in delayed messages
	for _, delayedMsg := range delayedMessages {
		delayedMsg.FillInBatchGasCost(batchFetcher)
	}
	return nil
}

func getBatchSeqNumFromSubmission(tx *types.Receipt, seqFilter *bridgegen.SequencerInboxFilterer) (uint64, error) {
	logs := tx.Logs

	sequencerBridgeABI, err := bridgegen.SequencerInboxMetaData.GetAbi()
	if err != nil {
		panic(err)
	}
	batchDeliveredID := sequencerBridgeABI.Events["SequencerBatchDelivered"].ID

	for _, log := range logs {
		// We just need SequencerBatchDelivered log here
		if log.Topics[0] != batchDeliveredID {
			continue
		}
		parsedLog, err := seqFilter.ParseSequencerBatchDelivered(*log)
		if err != nil {
			return 0, err
		}
		if !parsedLog.BatchSequenceNumber.IsUint64() {
			return 0, errors.New("sequencer inbox event has non-uint64 sequence number")
		}
		if !parsedLog.AfterDelayedMessagesRead.IsUint64() {
			return 0, errors.New("sequencer inbox event has non-uint64 delayed messages read")
		}

		seqNum := parsedLog.BatchSequenceNumber.Uint64()
		return seqNum, nil
	}
	return 0, ErrSubmissionTx
}

func getBatchBySeqNum(seqNum int64, seqFilter *bridgegen.SequencerInboxFilterer) (afterBatchDelayedCount uint64, err error) {
	fmt.Println("Searching batch of sequencer number:", seqNum)
	iter, err := seqFilter.FilterSequencerBatchDelivered(&bind.FilterOpts{}, []*big.Int{big.NewInt(seqNum)}, nil, nil)
	if err != nil {
		return 0, err
	}
	defer iter.Close()

	for iter.Next() {
		event := iter.Event
		if event.BatchSequenceNumber.Cmp(big.NewInt(seqNum)) == 0 {
			afterBatchDelayedCount = event.AfterDelayedMessagesRead.Uint64()
		}

	}

	if afterBatchDelayedCount == 0 {
		return 0, ErrBatchNotFound
	}

	return afterBatchDelayedCount, nil
}

func getAfterDelayedBySeqNum(seqNum int64, seqFilter *bridgegen.SequencerInboxFilterer) (afterBatchDelayedCount uint64, err error) {
	iter, err := seqFilter.FilterSequencerBatchDelivered(&bind.FilterOpts{}, []*big.Int{big.NewInt(seqNum)}, nil, nil)
	if err != nil {
		return 0, err
	}
	defer iter.Close()

	for iter.Next() {
		event := iter.Event
		if event.BatchSequenceNumber.Cmp(big.NewInt(seqNum)) == 0 {
			afterBatchDelayedCount = event.AfterDelayedMessagesRead.Uint64()
		}

	}

	if afterBatchDelayedCount == 0 {
		return 0, ErrBatchNotFound
	}

	return afterBatchDelayedCount, nil
}

func setDelayedToBackendByIndexRange(ctx context.Context, client *ethclient.Client, inboxAddress common.Address, bridgeAddress common.Address, fromIndex int64, toIndex int64, backend *MultiplexerBackend) error {
	// If no delayed messages, the fromIndex - 1 = toIndex
	if fromIndex-1 == toIndex {
		fmt.Println("No new delayed msg in current batch")
		return nil
	}
	parsedIBridgeABI, err := bridgegen.IBridgeMetaData.GetAbi()
	if err != nil {
		panic(err)
	}
	messageDeliveredID := parsedIBridgeABI.Events["MessageDelivered"].ID

	bridgeAddresses := []common.Address{bridgeAddress}

	query := ethereum.FilterQuery{
		BlockHash: nil,
		// FromBlock: new(big.Int).SetUint64(minBlockNum),
		// ToBlock:   new(big.Int).SetUint64(maxBlockNum),
		Addresses: bridgeAddresses,
		Topics:    [][]common.Hash{{messageDeliveredID}, {common.BigToHash(big.NewInt(fromIndex)), common.BigToHash(big.NewInt(toIndex))}},
	}
	logs, err := client.FilterLogs(ctx, query)

	var fromBlock uint64
	var toBlock uint64

	for _, log := range logs {
		if log.Topics[1] == common.BigToHash(big.NewInt(fromIndex)) {
			fromBlock = log.BlockNumber
		}
		if log.Topics[1] == common.BigToHash(big.NewInt(toIndex)) {
			toBlock = log.BlockNumber
		}
	}

	delayedBridge, err := arbnode.NewDelayedBridge(client, bridgeAddress, fromBlock-1)

	delayedMsg, err := delayedBridge.LookupMessagesInRange(ctx, big.NewInt(int64(fromBlock)), big.NewInt(int64(toBlock)), nil)
	if err != nil {
		return err
	}
	for _, msg := range delayedMsg {
		pos, err := msg.Message.Header.SeqNum()
		if err != nil {
			return err
		}
		backend.SetDelayedMsg(pos, msg.Message)
	}

	fmt.Println("We got delayed messages: ", len(delayedMsg))
	return nil
}

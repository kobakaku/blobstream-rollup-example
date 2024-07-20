package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"log"
	"math/big"

	wrapper "github.com/celestiaorg/blobstream-contracts/v4/wrappers/Blobstream.sol"
	"github.com/celestiaorg/celestia-app/pkg/square"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	ethcmn "github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/tendermint/tendermint/crypto/merkle"
	"github.com/tendermint/tendermint/rpc/client/http"
)

const (
	txHash                   = "4B122452FA679F15B458271512816B933803D5870919F67969B4D62221D70346"
	blobIndex                = 0
	rpcEndpoint              = "tcp://consensus.lunaroasis.net:26657"
	evmRPC                   = "https://eth-goerli.public.blastapi.io"
	contractAddr             = "0x046120E6c6C48C05627FB369756F5f44858950a5"
	dataCommitmentStartBlock = 105001
	dataCommitmentEndBlock   = 106001
	dataCommitmentNonce      = 106
)

func main() {
	log.Println("🐱 Starting the verification process...")

	if err := verify(); err != nil {
		log.Fatalf("❌ Verification process failed: %v", err)
	}

	log.Println("✅ Verification process completed successfully.")
}

func verify() error {
	log.Println("🔍 Decoding transaction hash...")

	txHashBz, err := hex.DecodeString(txHash)
	if err != nil {
		return fmt.Errorf("failed to decode transaction hash: %w", err)
	}

	log.Println("🔗 Establishing connection to Celestia...")
	trpc, err := http.New(rpcEndpoint, "/websocket")
	if err != nil {
		return fmt.Errorf("failed to connect to Celestia: %w", err)
	}
	defer trpc.Stop()

	ctx := context.Background()

	log.Println("📦 Fetching transaction with decoded hash from Celestia...")
	tx, err := trpc.Tx(ctx, txHashBz, true)
	if err != nil {
		return fmt.Errorf("failed to fetch transaction: %w", err)
	}

	log.Println("📦 Fetching block from Celestia...")
	block, err := trpc.Block(ctx, &tx.Height)
	if err != nil {
		return fmt.Errorf("failed to fetch block: %w", err)
	}

	// Calculate the range of shares that the blob toccupied in the block
	blobShareRange, err := square.BlobShareRange(block.Block.Txs.ToSliceOfBytes(), int(tx.Index), int(blobIndex), block.Block.Version.App)
	if err != nil {
		return fmt.Errorf("failed to get blob share range: %w", err)
	}

	shareProofs, err := trpc.ProveShares(ctx, uint64(tx.Height), uint64(blobShareRange.Start), uint64(blobShareRange.End))
	if err != nil {
		return fmt.Errorf("failed to get share proofs: %w", err)
	}

	log.Println("🔍 Verifying share proofs...")
	if !shareProofs.VerifyProof() {
		return fmt.Errorf("failed to verify share proofs: %w", err)
	}

	log.Println("🔍 Generating data root inclusion proof...")
	dcProof, err := trpc.DataRootInclusionProof(ctx, uint64(tx.Height), dataCommitmentStartBlock, dataCommitmentEndBlock)
	if err != nil {
		return fmt.Errorf("failed to generate data root inclusion proof: %w", err)
	}

	log.Println("🔗 Establishing connection to Ethereum client...")
	ethClient, err := ethclient.Dial(evmRPC)
	if err != nil {
		return fmt.Errorf("failed to connect to Ethereum client: %w", err)
	}
	defer ethClient.Close()

	log.Println("📦 Fetching BlobstreamX contract...")
	contractAddress := ethcmn.HexToAddress(contractAddr)
	blobstreamWrapper, err := wrapper.NewWrappers(contractAddress, ethClient)
	if err != nil {
		return fmt.Errorf("failed to fetch BlobstreamX contract: %w", err)
	}

	log.Println("🔍 Verifying data root inclusion on BlobstreamX contract...")
	VerifyDataRootInclusion(blobstreamWrapper, tx.Height, dataCommitmentNonce, block.Block.DataHash, dcProof.Proof)

	return nil
}

func VerifyDataRootInclusion(blobstreamWrapper *wrapper.Wrappers, height int64, nonce int, dataRoot []byte,
	proof merkle.Proof) (bool, error) {
	tuple := wrapper.DataRootTuple{
		Height:   big.NewInt(height),
		DataRoot: *(*[32]byte)(dataRoot),
	}

	sideNodes := make([][32]byte, len(proof.Aunts))
	for i, aunt := range proof.Aunts {
		sideNodes[i] = *(*[32]byte)(aunt)
	}
	wrappedProof := wrapper.BinaryMerkleProof{
		SideNodes: sideNodes,
		Key:       big.NewInt(proof.Index),
		NumLeaves: big.NewInt(proof.Total),
	}

	valid, err := blobstreamWrapper.VerifyAttestation(&bind.CallOpts{}, big.NewInt(int64(nonce)), tuple, wrappedProof)
	return valid, err
}

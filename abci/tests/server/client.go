package testsuite

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	abciclient "github.com/dashpay/tenderdash/abci/client"
	"github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/dash/llmq"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
)

func InitChain(ctx context.Context, client abciclient.Client) error {
	const total = 10
	ld, err := llmq.Generate(crypto.RandProTxHashes(total))
	if err != nil {
		return err
	}
	validatorSet, err := types.LLMQToValidatorSetProto(*ld)
	if err != nil {
		return err
	}
	_, err = client.InitChain(ctx, &types.RequestInitChain{
		ValidatorSet: validatorSet,
	})
	if err != nil {
		fmt.Printf("Failed test: InitChain - %v\n", err)
		return err
	}
	fmt.Println("Passed test: InitChain")
	return nil
}

func ProcessProposal(
	ctx context.Context,
	client abciclient.Client,
	statusExp types.ResponseProcessProposal_ProposalStatus,
	txBytes [][]byte,
	codeExp []uint32,
	dataExp []byte,
	hashExp []byte,
) error {
	res, err := client.ProcessProposal(ctx, &types.RequestProcessProposal{Txs: txBytes})
	if err != nil {
		return err
	}
	appHash := res.AppHash
	if !bytes.Equal(appHash, hashExp) {
		fmt.Println("Failed test: ProcessProposal")
		fmt.Printf("Application hash was unexpected. Got %X expected %X\n", appHash, hashExp)
		return errors.New("FinalizeBlock  error")
	}
	if res.Status != statusExp {
		fmt.Println("Failed test: ProcessProposal")
		fmt.Printf("ProcessProposal response status was unexpected. Got %v expected %v.",
			res.Status, statusExp)
		return errors.New("ProcessProposal error")
	}
	for i, tx := range res.TxResults {
		code, data, log := tx.Code, tx.Data, tx.Log
		if code != codeExp[i] {
			fmt.Println("Failed test: ProcessProposal")
			fmt.Printf("ProcessProposal response code was unexpected. Got %v expected %v. Log: %v\n",
				code, codeExp, log)
			return errors.New("ProcessProposal error")
		}
		if !bytes.Equal(data, dataExp) {
			fmt.Println("Failed test:  ProcessProposal")
			fmt.Printf("ProcessProposal response data was unexpected. Got %X expected %X\n",
				data, dataExp)
			return errors.New("ProcessProposal  error")
		}
	}
	fmt.Println("Passed test: ProcessProposal")
	return nil
}

func FinalizeBlock(ctx context.Context, client abciclient.Client, txBytes [][]byte) error {
	_, err := client.FinalizeBlock(ctx, &types.RequestFinalizeBlock{
		Block: &tmproto.Block{
			Data: tmproto.Data{Txs: txBytes},
		},
	})
	if err != nil {
		return err
	}
	fmt.Println("Passed test: FinalizeBlock")
	return nil
}

func PrepareProposal(ctx context.Context, client abciclient.Client, txBytes [][]byte, codeExp []types.TxRecord_TxAction, _dataExp []byte) error {
	res, _ := client.PrepareProposal(ctx, &types.RequestPrepareProposal{Txs: txBytes})
	for i, tx := range res.TxRecords {
		if tx.Action != codeExp[i] {
			fmt.Println("Failed test: PrepareProposal")
			fmt.Printf("PrepareProposal response code was unexpected. Got %v expected %v.",
				tx.Action, codeExp)
			return errors.New("PrepareProposal error")
		}
	}
	fmt.Println("Passed test: PrepareProposal")
	return nil
}

func CheckTx(ctx context.Context, client abciclient.Client, txBytes []byte, codeExp uint32, dataExp []byte) error {
	res, _ := client.CheckTx(ctx, &types.RequestCheckTx{Tx: txBytes})
	code, data := res.Code, res.Data
	if code != codeExp {
		fmt.Println("Failed test: CheckTx")
		fmt.Printf("CheckTx response code was unexpected. Got %v expected %v.,",
			code, codeExp)
		return errors.New("checkTx")
	}
	if !bytes.Equal(data, dataExp) {
		fmt.Println("Failed test: CheckTx")
		fmt.Printf("CheckTx response data was unexpected. Got %X expected %X\n",
			data, dataExp)
		return errors.New("checkTx")
	}
	fmt.Println("Passed test: CheckTx")
	return nil
}

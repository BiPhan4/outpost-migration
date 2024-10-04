package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/cosmos/gogoproto/proto"
	icatypes "github.com/cosmos/ibc-go/v7/modules/apps/27-interchain-accounts/types"
	"github.com/strangelove-ventures/interchaintest/v7/testutil"

	logger "github.com/JackalLabs/storage-outpost/e2e/interchaintest/logger"
	"github.com/JackalLabs/storage-outpost/e2e/interchaintest/testsuite"
	"github.com/JackalLabs/storage-outpost/e2e/interchaintest/types"

	storagetypes "github.com/JackalLabs/storage-outpost/e2e/interchaintest/storagetypes"
	outpostuser "github.com/JackalLabs/storage-outpost/e2e/interchaintest/types/outpostuser"
	"github.com/strangelove-ventures/interchaintest/v7/chain/cosmos"

	wasmtypes "github.com/CosmWasm/wasmd/x/wasm/types"
	testtypes "github.com/JackalLabs/storage-outpost/e2e/interchaintest/types"
)

// WARNING: strangelove's test package builds chains running ibc-go/v7
// Hopefully this won't cause issues because the canined image we use is running ibc-go/v4
// and packets should be consumed by the ica host no matter what version of ibc-go the controller chain is running

func (s *ContractTestSuite) TestOutpostUser() {
	ctx := context.Background()

	logger.InitLogger()

	// Need to instantiate it with the address of the outpost user as the owner

	encoding := icatypes.EncodingProtobuf
	// This starts the chains, relayer, creates the user accounts, creates the ibc clients and connections,
	// sets up the contract and does the channel handshake for the contract test suite.
	s.SetupContractTestSuite(ctx, encoding)
	wasmd, canined := s.ChainA, s.ChainB
	fmt.Println(wasmd)
	wasmdUser := s.UserA
	fmt.Println(wasmdUser)

	logger.LogInfo(canined.FullNodes)

	// Fund the ICA address:
	s.FundAddressChainB(ctx, s.IcaAddress)

	// Give canined some time to complete the handshake
	time.Sleep(time.Duration(30) * time.Second)

	// Let's go ahead and instantiate the outpost user, giving it the address of the outpost
	// Upload and Instantiate the contract on wasmd:
	codeId, err := s.ChainA.StoreContract(ctx, s.UserA.KeyName(), "../../artifacts/outpost_user.wasm")
	s.Require().NoError(err)

	// TODO: Can't init outpost user with outpost address - chicken and egg situation
	instantiateMsg := testtypes.NewInstantiateMsgWithOutpostAddress(&s.Contract.Address)

	outpostUserContract, err := s.ChainA.InstantiateContract(ctx, s.UserA.KeyName(), codeId, instantiateMsg, false, "--gas", "500000", "--admin", s.UserA.KeyName())
	logger.LogInfo(fmt.Sprintf("outpost user address is: %s\n", outpostUserContract))
	s.Require().NoError(err)

	// The 'Setup Function above' sets up relays, channels, and inits an outpost contract.
	// We don't want to change the above function because other tests rely on it
	// For this purposes of this test, we will init a brand new outpost with the address of 'outpost user' as the owner
	admin := s.UserA.FormattedAddress()

	// Instantiate the contract with channel:
	outpostInstantiateMsg := types.InitOutpostWithOwner(&admin, s.ChainAConnID, s.ChainBConnID, nil, &encoding, &outpostUserContract)

	// We know that wasm module of outpost has code ID 1
	outpostAddr, err := s.ChainA.InstantiateContract(ctx, s.UserA.KeyName(), "1", outpostInstantiateMsg, false, "--gas", "500000", "--admin", s.UserA.KeyName())
	s.Require().NoError(err)

	// Update test suite with new outpost address
	s.Contract.Address = outpostAddr

	logger.LogInfo(fmt.Sprintf("The outpost address is: %s\n", outpostAddr))

	// Query the outpost owner
	outpostOwner, ownerErr := testsuite.GetOutpostOwner(ctx, s.ChainA, outpostAddr)
	s.Require().NoError(ownerErr)
	logger.LogInfo(fmt.Sprintf("The outpost owner is: %s\n", outpostOwner))

	// Wait for the new channel to get set up
	err = testutil.WaitForBlocks(ctx, 5, s.ChainA, s.ChainB)
	s.Require().NoError(err)

	contractState, err := s.Contract.QueryContractState(ctx)
	s.Require().NoError(err)

	logger.LogInfo(fmt.Sprintf("The outpost state is: %v\n", contractState))
	logger.LogInfo(fmt.Sprintf("The outpost jkl (ica) address is: %s\n", contractState.IcaInfo.IcaAddress))

	// NOTE: note sure if Jackal Outpost needs the ownership feature
	// ownershipResponse, err := s.Contract.QueryOwnership(ctx)
	// s.Require().NoError(err)

	s.IcaAddress = contractState.IcaInfo.IcaAddress
	s.Contract.SetIcaAddress(s.IcaAddress)

	s.Run(fmt.Sprintf("TestOutpostUserSuccess-%s", encoding), func() {

		saveOutpostMsg := outpostuser.ExecuteMsg_SaveOutpost{
			Address: s.Contract.Address,
		}

		outpostUserMsg0 := outpostuser.ExecuteMsg{
			SaveOutpost: &saveOutpostMsg,
		}

		res, err := s.ChainA.ExecuteContract(ctx, s.UserA.KeyName(), outpostUserContract, outpostUserMsg0.ToString(), "--gas", "500000")
		fmt.Println(res)
		s.Require().NoError(err)

		merkleBytes := []byte{0x01, 0x02, 0x03, 0x04}
		postFileMsg := &storagetypes.MsgPostFile{
			Creator:       s.Contract.IcaAddress,
			Merkle:        merkleBytes,
			FileSize:      100000000,
			ProofInterval: 60,
			ProofType:     1,
			MaxProofs:     100,
			Expires:       100 + ((100 * 365 * 24 * 60 * 60) / 6),
			Note:          `{"description": "outpost user note", "additional_info": "placeholder"}`,
		}

		typeURL := "/canine_chain.storage.MsgPostFile"

		sendStargateMsg := testtypes.NewExecuteMsg_SendCosmosMsgs_FromProto(
			[]proto.Message{postFileMsg}, nil, nil, typeURL,
		)

		// NOTE: Double check this before calling it
		innerOutpostMsg := outpostuser.ExecuteMsg_CallOutpost{
			Msg: &sendStargateMsg,
		}

		outpostUserMsg := outpostuser.ExecuteMsg{
			CallOutpost: &innerOutpostMsg,
		}

		// WARNING NOTE: Only the owner of the outpost can call it.
		// The below execution doesn't work because cross contract calls are made with the calling contract's address as the sender
		// Unfortunately, UserA is set as the outpost owner because UserA instantiated it
		// Seems there's no way around this but to have the outpost user contract also instantiate the outpost

		// We know 'instantiate2' works on canine-chain, so perhaps we can use that and avoid having to use a callback
		// badRes, err := s.ChainA.ExecuteContract(ctx, s.UserA.KeyName(), outpostUserContract, outpostUserMsg.ToString(), "--gas", "500000")
		// s.Require().NoError(err)
		// fmt.Println(badRes)

		// We try to use this broadcaster immediately at the same time as the above execute, but the above execute may not have been committed
		// yet
		// let's wait a few blocks
		err = testutil.WaitForBlocks(ctx, 5, s.ChainA, s.ChainB)
		s.Require().NoError(err)

		fmt.Println("*******NOW BROADCASTING***************")
		outpostUserMsgBz, err := json.Marshal(outpostUserMsg)
		s.Require().NoError(err)

		b := cosmos.NewBroadcaster(s.T(), s.ChainA)
		executeMsg := &wasmtypes.MsgExecuteContract{
			Sender:   s.UserA.FormattedAddress(),
			Contract: outpostUserContract,
			Msg:      outpostUserMsgBz,
		}
		resp, err := cosmos.BroadcastTx(ctx, b, s.UserA, executeMsg)
		s.Require().NoError(err)

		/*
					NOTE:

					We're getting this classic error:

					*******NOW BROADCASTING***************
			=== NAME  TestWithContractTestSuite/TestOutpostUser/TestOutpostUserSuccess-proto3
			    outpost_user_test.go:172:
			                Error Trace:    /Users/biphan/jackal/storage-outpost/e2e/interchaintest/outpost_user_test.go:172
			                                                        /Users/biphan/go/pkg/mod/github.com/stretchr/testify@v1.8.4/suite/suite.go:112
			                Error:          Received unexpected error:
			                                invalid Bech32 prefix; expected cosmos, got wasm
			                Test:           TestWithContractTestSuite/TestOutpostUser/TestOutpostUserSuccess-proto3
		*/

		fmt.Println(resp.TxHash)

	},
	)
	// implement mock query server
	time.Sleep(time.Duration(10) * time.Hour)
}

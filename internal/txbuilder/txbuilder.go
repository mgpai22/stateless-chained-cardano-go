package txbuilder

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"

	"github.com/Salvionied/apollo"
	"github.com/Salvionied/apollo/constants"
	serAddress "github.com/Salvionied/apollo/serialization/Address"
	"github.com/Salvionied/apollo/serialization/Asset"
	"github.com/Salvionied/apollo/serialization/AssetName"
	"github.com/Salvionied/apollo/serialization/Key"
	"github.com/Salvionied/apollo/serialization/MultiAsset"
	"github.com/Salvionied/apollo/serialization/Policy"
	"github.com/Salvionied/apollo/serialization/Transaction"
	"github.com/Salvionied/apollo/serialization/TransactionInput"
	"github.com/Salvionied/apollo/serialization/TransactionOutput"
	"github.com/Salvionied/apollo/serialization/UTxO"
	"github.com/Salvionied/apollo/serialization/Value"
	"github.com/Salvionied/apollo/txBuilding/Backend/BlockFrostChainContext"
	"github.com/SundaeSwap-finance/kugo"
	"github.com/zenGate-Global/stateless/internal/config"
	"github.com/zenGate-Global/stateless/internal/wallet"
)

func BuildRewardTx(
	lovelace int,
	address string,
) (*Transaction.Transaction, error) {
	var err error
	w := wallet.GetWallet()
	if w == nil {
		return nil, errors.New("cannot initialize wallet")
	}
	cc := apollo.NewEmptyBackend()
	apollob := apollo.New(&cc)
	apollob, err = apollob.
		SetWalletFromBech32(w.PaymentAddress).
		SetWalletAsChangeAddress()
	if err != nil {
		return nil, err
	}

	utxos, err := getUtxosByAddress(w.PaymentAddress)
	if err != nil {
		return nil, err
	}
	apollob = apollob.AddLoadedUTxOs(utxos...)

	apollob = apollob.
		PayToAddressBech32(
			address,
			lovelace,
		)
	tx, err := apollob.Complete()
	if err != nil {
		return nil, err
	}
	vKeyBytes, err := hex.DecodeString(w.PaymentVKey.CborHex)
	if err != nil {
		return nil, err
	}
	sKeyBytes, err := hex.DecodeString(w.PaymentExtendedSKey.CborHex)
	if err != nil {
		return nil, err
	}
	// Strip off leading 2 bytes as shortcut for CBOR decoding to unwrap bytes
	vKeyBytes = vKeyBytes[2:]
	sKeyBytes = sKeyBytes[2:]
	// Strip out public key portion of extended private key
	sKeyBytes = slices.Delete(sKeyBytes, 64, 96)
	vkey := Key.VerificationKey{Payload: vKeyBytes}
	skey := Key.SigningKey{Payload: sKeyBytes}
	tx, err = tx.SignWithSkey(vkey, skey)
	if err != nil {
		return nil, err
	}
	return tx.GetTx(), nil
}

func getBlockfrostContext() (*BlockFrostChainContext.BlockFrostChainContext, error) {
	cfg := config.GetConfig()
	var ret BlockFrostChainContext.BlockFrostChainContext
	var err error
	switch cfg.Network {
	case "preprod":
		ret, err = BlockFrostChainContext.NewBlockfrostChainContext(
			constants.BLOCKFROST_BASE_URL_PREPROD,
			int(constants.PREPROD),
			cfg.TxBuilder.BlockfrostApiKey,
		)
		if err != nil {
			return nil, err
		}
	// TODO: add more networks
	default:
		return nil, fmt.Errorf("unsupported network: %s", cfg.Network)
	}
	return &ret, nil
}

func getKupoClient() (*kugo.Client, error) {
	cfg := config.GetConfig()
	if cfg.TxBuilder.KupoUrl == "" {
		return nil, errors.New("no kupo url provided")
	}
	k := kugo.New(
		kugo.WithEndpoint(cfg.TxBuilder.KupoUrl),
	)
	if k == nil {
		return nil, fmt.Errorf("failed kupo client: %s", cfg.TxBuilder.KupoUrl)
	}
	return k, nil
}

func getUtxosByAddress(addr string) ([]UTxO.UTxO, error) {
	cfg := config.GetConfig()
	if cfg.TxBuilder.BlockfrostApiKey != "" {
		bfc, err := getBlockfrostContext()
		if err != nil {
			return nil, err
		}
		serAddr, err := serAddress.DecodeAddress(addr)
		if err != nil {
			return nil, err
		}
		utxos, err := bfc.Utxos(serAddr)
		if err != nil {
			return nil, err
		}
		return utxos, nil
	} else if cfg.TxBuilder.KupoUrl != "" {
		k, err := getKupoClient()
		if err != nil {
			return nil, err
		}
		matches, err := k.Matches(
			context.Background(),
			kugo.Pattern(addr),
		)
		if err != nil {
			return nil, err
		}
		var ret []UTxO.UTxO
		for _, match := range matches {
			tmpUtxo := kupoMatchToApolloUtxo(match)
			ret = append(ret, tmpUtxo)
		}
		return ret, nil
	} else if cfg.TxBuilder.CardanoMonitorUrl != "" {
		slog.Info("using cardano monitor url")
		utxos, err := getCardanoMonitorUtxos(addr)
		if err != nil {
			return nil, err
		}
		return utxos, nil
	}
	return nil, errors.New("no valid Blockfrost or Kupo/Ogmios config found")
}

// CardanoMonitorRequest represents the request structure for Cardano Monitor API
type CardanoMonitorRequest struct {
	Address        string `json:"address"`
	Mode           string `json:"mode"`
	Query          string `json:"query"`
	Offset         int    `json:"offset"`
	Limit          int    `json:"limit"`
	IncludeCborHex bool   `json:"includeCborHex"`
}

// CardanoMonitorUtxo represents a UTxO from Cardano Monitor API
type CardanoMonitorUtxo struct {
	TransactionHash string `json:"transactionHash"`
	Index           uint32 `json:"index"`
	Cbor            string `json:"cbor"`
}

// CardanoMonitorResponse represents the response from Cardano Monitor API
type CardanoMonitorResponse struct {
	Success bool                 `json:"success"`
	Data    []CardanoMonitorUtxo `json:"data"`
	Error   *string              `json:"error,omitempty"`
}

func getCardanoMonitorUtxos(addr string) ([]UTxO.UTxO, error) {
	cfg := config.GetConfig()
	if cfg.TxBuilder.CardanoMonitorUrl == "" {
		return nil, errors.New("no cardano monitor url provided")
	}

	var allUtxos []UTxO.UTxO
	offset := 0
	limit := 100

	// Keep fetching until we get all UTxOs
	for {
		requestPayload := CardanoMonitorRequest{
			Address:        addr,
			Mode:           "byPaymentCredential",
			Query:          "unspent",
			Offset:         offset,
			Limit:          limit,
			IncludeCborHex: true,
		}

		jsonData, err := json.Marshal(requestPayload)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request: %w", err)
		}

		req, err := http.NewRequest(
			http.MethodPost,
			cfg.TxBuilder.CardanoMonitorUrl+"/getUtxos",
			bytes.NewBuffer(jsonData),
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}

		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("failed to make request: %w", err)
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("failed to read response: %w", err)
		}

		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, fmt.Errorf(
				"API request failed with status %d: %s",
				resp.StatusCode,
				string(body),
			)
		}

		var apiResponse []CardanoMonitorUtxo
		if err := json.Unmarshal(body, &apiResponse); err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("failed to unmarshal response: %w", err)
		}

		resp.Body.Close()

		// Convert and add UTxOs from this batch
		for _, cmUtxo := range apiResponse {
			apolloUtxo, err := convertCardanoMonitorUtxo(cmUtxo)
			if err != nil {
				return nil, fmt.Errorf("failed to convert UTxO: %w", err)
			}
			allUtxos = append(allUtxos, *apolloUtxo)
		}

		// If we got fewer results than requested, we've reached the end
		if len(apiResponse) < limit {
			break
		}

		// Move to next batch
		offset += limit
	}

	return allUtxos, nil
}

func convertCardanoMonitorUtxo(cmUtxo CardanoMonitorUtxo) (*UTxO.UTxO, error) {
	var utxo UTxO.UTxO
	txHex, err := hex.DecodeString(cmUtxo.TransactionHash)
	if err != nil {
		return nil, fmt.Errorf(
			"failed to decode UTxO transaction hash: %w",
			err,
		)
	}
	utxo.Input = TransactionInput.TransactionInput{
		TransactionId: txHex,
		Index:         int(cmUtxo.Index),
	}
	tmpOutput := TransactionOutput.TransactionOutput{}
	cborHex, err := hex.DecodeString(cmUtxo.Cbor)
	if err != nil {
		return nil, fmt.Errorf("failed to decode UTxO cbor: %w", err)
	}
	err = tmpOutput.UnmarshalCBOR(cborHex)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal UTxO output: %w", err)
	}
	utxo.Output = tmpOutput
	return &utxo, nil
}

func kupoMatchToApolloUtxo(match kugo.Match) UTxO.UTxO {
	serAddr, _ := serAddress.DecodeAddress(match.Address)
	txIdBytes, _ := hex.DecodeString(match.TransactionID)
	multiAssets := make(MultiAsset.MultiAsset[int64])
	totalLovelace := uint64(0)
	for policyId, assets := range match.Value {
		for assetId, assetAmount := range assets {
			if policyId == "ada" && assetId == "lovelace" {
				totalLovelace = assetAmount.Uint64()
				continue
			}
			tmpPolicyId := Policy.PolicyId{Value: policyId}
			tmpAssetName := AssetName.NewAssetNameFromString(assetId)
			if _, ok := multiAssets[tmpPolicyId]; !ok {
				multiAssets[tmpPolicyId] = Asset.Asset[int64]{}
			}
			multiAssets[tmpPolicyId][tmpAssetName] = assetAmount.Int64()
		}
	}
	val := Value.SimpleValue(
		// all the lovelace wouldn't overflow this
		int64(totalLovelace), // #nosec G115
		multiAssets,
	)
	ret := UTxO.UTxO{
		Input: TransactionInput.TransactionInput{
			TransactionId: txIdBytes,
			Index:         match.OutputIndex,
		},
		Output: TransactionOutput.SimpleTransactionOutput(
			serAddr,
			val,
		),
	}
	return ret
}

package main

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/blinklabs-io/buidler-fest-2024-workshop/internal/config"
	"github.com/blinklabs-io/buidler-fest-2024-workshop/internal/txbuilder"
	"github.com/blinklabs-io/buidler-fest-2024-workshop/internal/txsubmit"
	"github.com/blinklabs-io/buidler-fest-2024-workshop/internal/wallet"
	"github.com/blinklabs-io/gouroboros/ledger"
	"github.com/spf13/cobra"
)

const (
	programName                 = "stateless"
	submissionRetryCooldown     = 200 * time.Millisecond
	duplicateInputRetryCooldown = 200 * time.Millisecond
)

func main() {
	cmd := &cobra.Command{
		Use:  programName,
		Args: cobra.ExactArgs(0),
		Run:  workshopRun,
	}

	// Add flags for amount, address and config file
	cmd.Flags().String("amount", "1000000", "Lovelace amount to send (default: 1,000,000)")
	cmd.Flags().String("address", "", "Destination address (default: wallet payment address)")
	cmd.Flags().StringP("config", "c", "", "Path to YAML configuration file")
	cmd.Flags().Bool("submit", false, "Submit the transaction to the network (default: false, dry-run mode)")
	cmd.Flags().Int("repeat", 1, "Number of times to build (and submit if enabled) the transaction (default: 1)")
	cmd.Flags().String("build-cooldown", "0ms", "Cooldown between transaction builds (e.g., 0ms, 500ms, 2s)")

	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func workshopRun(cmd *cobra.Command, args []string) {
	// Configure logger with both stderr and file output
	logFile, err := os.OpenFile("stateless.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		slog.Error(fmt.Sprintf("failed to open log file: %s", err))
		os.Exit(1)
	}
	defer logFile.Close()

	// Create a multi-writer that writes to both stderr and the log file
	multiWriter := io.MultiWriter(os.Stderr, logFile)
	logger := slog.New(slog.NewTextHandler(multiWriter, nil))
	slog.SetDefault(logger)

	// Extract config file path from flags
	configPath, _ := cmd.Flags().GetString("config")

	// Load config
	if configPath != "" {
		// Load from specified config file
		_, err = config.LoadWithConfigFile(configPath)
		slog.Info(fmt.Sprintf("loaded configuration from: %s", configPath))
	} else {
		// Use default loading (checks for config.yaml or falls back to env vars)
		_, err = config.Load()
	}

	if err != nil {
		slog.Error(
			fmt.Sprintf("failed to load config: %s", err),
		)
		os.Exit(1)
	}
	// Setup wallet
	w, err := wallet.Setup()
	if err != nil {
		slog.Error(
			fmt.Sprintf("failed to configure wallet: %s", err),
		)
		os.Exit(1)
	}
	slog.Info(
		"loaded mnemonic for address: " + w.PaymentAddress,
	)

	// Extract lovelace amount, address, and submit flag from command line flags
	amountStr, _ := cmd.Flags().GetString("amount")
	address, _ := cmd.Flags().GetString("address")
	submit, _ := cmd.Flags().GetBool("submit")
	repeatCount, _ := cmd.Flags().GetInt("repeat")
	buildCooldownStr, _ := cmd.Flags().GetString("build-cooldown")

	// Convert amount string to int
	amount, err := strconv.Atoi(amountStr)
	if err != nil {
		slog.Error(
			fmt.Sprintf("invalid amount '%s': %s", amountStr, err),
		)
		os.Exit(1)
	}

	if repeatCount < 1 {
		slog.Error(
			fmt.Sprintf("repeat count must be at least 1 (got %d)", repeatCount),
		)
		os.Exit(1)
	}

	buildCooldown, err := time.ParseDuration(buildCooldownStr)
	if err != nil {
		slog.Error(
			fmt.Sprintf("invalid build cooldown '%s': %s", buildCooldownStr, err),
		)
		os.Exit(1)
	}

	if buildCooldown < 0 {
		slog.Error(
			fmt.Sprintf("build cooldown must be non-negative (got %s)", buildCooldown),
		)
		os.Exit(1)
	}

	// Use wallet payment address as default if no address provided
	if address == "" {
		address = w.PaymentAddress
	}

	slog.Info(
		fmt.Sprintf("sending %d lovelace to address: %s", amount, address),
	)

	if !submit {
		slog.Info("dry-run mode: transaction built but not submitted (use --submit to submit)")
	}

	usedInputs := make(map[string]struct{})

	for completed := 0; completed < repeatCount; completed++ {
		iteration := completed + 1
		slog.Info(fmt.Sprintf("building transaction %d of %d", iteration, repeatCount))

		var txBytes []byte

		for {
			tx, buildErr := txbuilder.BuildRewardTx(uint64(amount), address)
			if buildErr != nil {
				slog.Error(
					fmt.Sprintf("failed to build reward tx: %s", buildErr),
				)
				os.Exit(1)
			}

			txBytes, err = tx.Bytes()
			if err != nil {
				slog.Error(
					fmt.Sprintf("failed to marshal tx: %s", err),
				)
				os.Exit(1)
			}

			gouroborosDecodedTx, decodeErr := ledger.NewTransactionFromCbor(ledger.TxTypeConway, txBytes)

			if decodeErr != nil {
				slog.Error(
					fmt.Sprintf("failed to decode tx: %s", decodeErr),
				)
				os.Exit(1)
			}

			duplicateInputs := make([]string, 0)
			inputs := make([]string, 0, len(gouroborosDecodedTx.Inputs()))

			for _, input := range gouroborosDecodedTx.Inputs() {
				formatted := fmt.Sprintf("%s:%d", input.Id().String(), input.Index())
				if _, exists := usedInputs[formatted]; exists {
					duplicateInputs = append(duplicateInputs, formatted)
				}
				inputs = append(inputs, formatted)
			}

			if len(duplicateInputs) > 0 {
				slog.Warn(
					fmt.Sprintf("duplicate tx input found (iteration %d): %v", iteration, duplicateInputs),
				)
				slog.Info(
					fmt.Sprintf("retrying transaction build after %s due to duplicate inputs", duplicateInputRetryCooldown),
				)
				time.Sleep(duplicateInputRetryCooldown)
				continue
			}

			for _, input := range inputs {
				usedInputs[input] = struct{}{}
			}

			break
		}

		slog.Info(
			fmt.Sprintf("iteration %d tx bytes: %x", iteration, txBytes),
		)

		if submit {
			for {
				slog.Info(
					fmt.Sprintf("submitting transaction to network (iteration %d)...", iteration),
				)
				err = txsubmit.SubmitTx(txBytes)
				if err != nil {
					slog.Warn(
						fmt.Sprintf("failed to submit tx on iteration %d: %s", iteration, err),
					)
					slog.Info(
						fmt.Sprintf("retrying transaction submission after %s", submissionRetryCooldown),
					)
					time.Sleep(submissionRetryCooldown)
					continue
				}
				slog.Info(
					fmt.Sprintf("transaction submitted successfully (iteration %d)", iteration),
				)
				break
			}
		} else {
			slog.Info(
				fmt.Sprintf("transaction built (iteration %d)", iteration),
			)
		}

		if completed < repeatCount-1 && buildCooldown > 0 {
			slog.Info(
				fmt.Sprintf("waiting %s before next transaction build", buildCooldown),
			)
			time.Sleep(buildCooldown)
		}
	}
}

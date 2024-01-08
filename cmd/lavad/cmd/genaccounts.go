package cmd

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	"github.com/cosmos/cosmos-sdk/server"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	authvesting "github.com/cosmos/cosmos-sdk/x/auth/vesting/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/cosmos/cosmos-sdk/x/genutil"
	genutiltypes "github.com/cosmos/cosmos-sdk/x/genutil/types"
	"github.com/spf13/cobra"
)

const (
	flagVestingStart   = "vesting-start-time"
	flagVestingEnd     = "vesting-end-time"
	flagVestingAmt     = "vesting-amount"
	flagModuleAccount  = "module-account"
	flagPeriodicLength = "periodic-length"
	flagPeriodicNumber = "periodic-number"
	flagPeriodicFirst  = "periodic-first"
)

// AddGenesisAccountCmd returns add-genesis-account cobra Command.
func AddGenesisAccountCmd(defaultNodeHome string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add-genesis-account [address_or_key_name] [coin][,[coin]]",
		Short: "Add a genesis account to genesis.json",
		Long: `Add a genesis account to genesis.json. The provided account must specify
the account address or key name and a list of initial coins. If a key name is given,
the address will be looked up in the local Keybase. The list of initial tokens must
contain valid denominations. Accounts may optionally be supplied with vesting parameters.
lavad add-genesis-account bela 30000000ulava  --vesting-start-time 1704707673 --vesting-amount 1000ulava --periodic-number 10  --periodic-length 1 --periodic-first 200ulava
`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return err
			}
			cdc := clientCtx.Codec

			serverCtx := server.GetServerContextFromCmd(cmd)
			config := serverCtx.Config

			config.SetRoot(clientCtx.HomeDir)

			coins, err := sdk.ParseCoinsNormalized(args[1])
			if err != nil {
				return fmt.Errorf("failed to parse coins: %w", err)
			}

			createModuleAccount, err := cmd.Flags().GetBool(flagModuleAccount)
			if err != nil {
				return err
			}

			addr, err := sdk.AccAddressFromBech32(args[0])
			if err != nil && !createModuleAccount {
				inBuf := bufio.NewReader(cmd.InOrStdin())
				keyringBackend, err := cmd.Flags().GetString(flags.FlagKeyringBackend)
				if err != nil {
					return err
				}

				// attempt to lookup address from Keybase if no address was provided
				kb, err := keyring.New(sdk.KeyringServiceName(), keyringBackend, clientCtx.HomeDir, inBuf, clientCtx.Codec)
				if err != nil {
					return err
				}

				info, err := kb.Key(args[0])
				if err != nil {
					return fmt.Errorf("failed to get address from Keybase: %w", err)
				}

				addr, err = info.GetAddress()
				if err != nil {
					return fmt.Errorf("failed to get address from Info: %w", err)
				}
			}

			vestingStart, err := cmd.Flags().GetInt64(flagVestingStart)
			if err != nil {
				return err
			}
			vestingEnd, err := cmd.Flags().GetInt64(flagVestingEnd)
			if err != nil {
				return err
			}
			vestingAmtStr, err := cmd.Flags().GetString(flagVestingAmt)
			if err != nil {
				return err
			}

			vestingAmt, err := sdk.ParseCoinsNormalized(vestingAmtStr)
			if err != nil {
				return fmt.Errorf("failed to parse vesting amount: %w", err)
			}

			periodicLength, err := cmd.Flags().GetInt64(flagPeriodicLength)
			if err != nil {
				return err
			}

			periodicNumber, err := cmd.Flags().GetInt64(flagPeriodicNumber)
			if err != nil {
				return err
			}

			periodicFirstStr, err := cmd.Flags().GetString(flagVestingAmt)
			if err != nil {
				return err
			}

			periodicFirst, err := sdk.ParseCoinsNormalized(periodicFirstStr)
			if err != nil {
				return fmt.Errorf("failed to parse periodicFirst amount: %w", err)
			}

			// create concrete account type based on input parameters
			var genAccount authtypes.GenesisAccount

			balances := banktypes.Balance{Address: addr.String(), Coins: coins.Sort()}
			baseAccount := authtypes.NewBaseAccount(addr, nil, 0, 0)
			if createModuleAccount {
				moduleAddress := authtypes.NewModuleAddress(args[0]).String()
				baseAccount.Address = moduleAddress
				balances.Address = moduleAddress
				genAccount = authtypes.NewModuleAccount(baseAccount, args[0], authtypes.Burner, authtypes.Staking)
			} else if !vestingAmt.IsZero() {
				baseVestingAccount := authvesting.NewBaseVestingAccount(baseAccount, vestingAmt.Sort(), vestingEnd)

				if (balances.Coins.IsZero() && !baseVestingAccount.OriginalVesting.IsZero()) ||
					baseVestingAccount.OriginalVesting.IsAnyGT(balances.Coins) {
					return errors.New("vesting amount cannot be greater than total amount")
				}

				switch {
				case periodicLength != 0 || periodicNumber != 0:
					if periodicLength <= 0 {
						return errors.New("periodic account must set periodicLength flag")
					}

					if periodicNumber <= 0 {
						return errors.New("periodic account must set periodicNumber flag")
					}

					if vestingStart <= 0 {
						return errors.New("periodic account must have vesting start flag")
					}

					if err := periodicFirst.Validate(); err != nil {
						return errors.New("periodic account must have vesting first emission none negative " + err.Error())
					}
					if !vestingAmt.QuoInt(sdk.NewInt(periodicNumber)).MulInt(sdk.NewInt(periodicNumber)).IsEqual(vestingAmt) {
						return errors.New("periodic vesting amount must be divisble by the periodicNumber")
					}

					periods := []authvesting.Period{{Length: 0, Amount: periodicFirst}}
					for i := int64(0); i < periodicNumber; i++ {
						period := authvesting.Period{Length: periodicLength, Amount: vestingAmt.QuoInt(sdk.NewInt(periodicNumber))}
						periods = append(periods, period)
					}

					genAccount = authvesting.NewPeriodicVestingAccount(baseAccount, vestingAmt.Add(periodicFirst...), vestingStart, periods)
				case vestingStart != 0 && vestingEnd != 0:
					genAccount = authvesting.NewContinuousVestingAccountRaw(baseVestingAccount, vestingStart)

				case vestingEnd != 0:
					genAccount = authvesting.NewDelayedVestingAccountRaw(baseVestingAccount)

				default:
					return errors.New("invalid vesting parameters; must supply start and end time or end time")
				}
			} else {
				genAccount = baseAccount
			}

			if err := genAccount.Validate(); err != nil {
				return fmt.Errorf("failed to validate new genesis account: %w", err)
			}

			genFile := config.GenesisFile()
			appState, genDoc, err := genutiltypes.GenesisStateFromGenFile(genFile)
			if err != nil {
				return fmt.Errorf("failed to unmarshal genesis state: %w", err)
			}

			authGenState := authtypes.GetGenesisStateFromAppState(cdc, appState)

			accs, err := authtypes.UnpackAccounts(authGenState.Accounts)
			if err != nil {
				return fmt.Errorf("failed to get accounts from any: %w", err)
			}

			if accs.Contains(addr) {
				return fmt.Errorf("cannot add account at existing address %s", addr)
			}

			// Add the new account to the set of genesis accounts and sanitize the
			// accounts afterwards.
			accs = append(accs, genAccount)
			accs = authtypes.SanitizeGenesisAccounts(accs)

			genAccs, err := authtypes.PackAccounts(accs)
			if err != nil {
				return fmt.Errorf("failed to convert accounts into any's: %w", err)
			}
			authGenState.Accounts = genAccs

			authGenStateBz, err := cdc.MarshalJSON(&authGenState)
			if err != nil {
				return fmt.Errorf("failed to marshal auth genesis state: %w", err)
			}

			appState[authtypes.ModuleName] = authGenStateBz

			bankGenState := banktypes.GetGenesisStateFromAppState(cdc, appState)
			bankGenState.Balances = append(bankGenState.Balances, balances)
			bankGenState.Balances = banktypes.SanitizeGenesisBalances(bankGenState.Balances)

			bankGenStateBz, err := cdc.MarshalJSON(bankGenState)
			if err != nil {
				return fmt.Errorf("failed to marshal bank genesis state: %w", err)
			}

			appState[banktypes.ModuleName] = bankGenStateBz

			appStateJSON, err := json.Marshal(appState)
			if err != nil {
				return fmt.Errorf("failed to marshal application genesis state: %w", err)
			}

			genDoc.AppState = appStateJSON
			return genutil.ExportGenesisFile(genDoc, genFile)
		},
	}

	cmd.Flags().String(flags.FlagKeyringBackend, flags.DefaultKeyringBackend, "Select keyring's backend (os|file|kwallet|pass|test)")
	cmd.Flags().String(flags.FlagHome, defaultNodeHome, "The application home directory")
	cmd.Flags().String(flagVestingAmt, "", "amount of coins for vesting accounts")
	cmd.Flags().Int64(flagVestingStart, 0, "schedule start time (unix epoch) for vesting accounts")
	cmd.Flags().Int64(flagVestingEnd, 0, "schedule end time (unix epoch) for vesting accounts")
	cmd.Flags().Bool(flagModuleAccount, false, "create a module account")
	cmd.Flags().Int64(flagPeriodicLength, 0, "length of the each period")
	cmd.Flags().Int64(flagPeriodicNumber, 0, "number of periods")
	cmd.Flags().String(flagPeriodicFirst, "", "the amount to be paid in the first emission")

	flags.AddQueryFlagsToCmd(cmd)

	return cmd
}

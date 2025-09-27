# Stateless Chained Transaction Go Example

This cli tool allows you to continuously build and submit transactions without waiting for the transaction to be confirmed statelessly.

Note the backend [cardano utxo monitor](https://github.com/zenGate-Global/cardano-utxo-monitor) is required to be set as the backend in the config file.

The transactions also must be submitted to the same node cardano utxo monitor is set up on.

This [tx submit](https://github.com/zenGate-Global/cardano-tx-submit) can be used.

## Usage

Make sure you create a `config.yaml`, see `config.example.yaml` for structure. 

Only the `submit url`, `cardano_monitor_url` and `mnemonic` needs to be set, the rest should remain as empty strings.

Compile the cli tool
```
make
```

Run it with 
```
./stateless
```

## Command Line Flags

The `stateless` command supports the following flags:

### Basic Options
- `--amount <value>` - Lovelace amount to send (default: `1000000` = 1,000,000 lovelace)
- `--address <address>` - Destination address (default: sends back to your own wallet)
- `--config <path>` or `-c <path>` - Path to YAML configuration file (default: looks for `config.yaml`)
- `--log-file <path>` - Path to log file (default: no file logging, stderr only)

### Submission Options  
- `--submit` - Submit the transaction to the network (default: `false`, runs in dry-run mode)
- `--repeat <count>` - Number of times to build (and submit if enabled) the transaction (default: `1`)
- `--build-cooldown <duration>` - Cooldown between transaction builds (default: `0ms`)
  - Examples: `0ms`, `500ms`, `2s`, `1m`

## Examples

Basic dry run (builds transaction but doesn't submit):
```bash
./stateless --amount 2000000 --address addr1...
```

Submit a single transaction:
```bash
./stateless --amount 1500000 --submit
```

Build and submit 5 transactions with 2 second cooldown between builds:
```bash
./stateless --repeat 5 --build-cooldown 2s --submit
```

Use custom configuration file:
```bash
./stateless -c /path/to/custom-config.yaml --submit
```

Log to a custom file while running:
```bash
./stateless --log-file stateless.log --repeat 3 --submit
```
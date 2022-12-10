module github.com/breez/lntest

go 1.18

require (
	github.com/btcsuite/btcd v0.23.3
	github.com/btcsuite/btcd/btcec/v2 v2.2.1
	github.com/btcsuite/btcd/btcutil v1.1.2
	github.com/decred/dcrd/dcrec/secp256k1/v4 v4.0.1
	github.com/lightningnetwork/lnd v0.15.4-beta
	github.com/niftynei/glightning v0.8.2
	go.uber.org/multierr v1.6.0
	golang.org/x/exp v0.0.0-20221012211006-4de253d81b95
	google.golang.org/grpc v1.50.1
	google.golang.org/protobuf v1.27.1
)

require (
	github.com/Yawning/aez v0.0.0-20211027044916-e49e68abd344 // indirect
	github.com/aead/siphash v1.0.1 // indirect
	github.com/btcsuite/btcd/chaincfg/chainhash v1.0.1 // indirect
	github.com/btcsuite/btclog v0.0.0-20170628155309-84c8d2346e9f // indirect
	github.com/btcsuite/btcwallet v0.16.1 // indirect
	github.com/btcsuite/btcwallet/walletdb v1.4.0 // indirect
	github.com/decred/dcrd/crypto/blake256 v1.0.0 // indirect
	github.com/golang/protobuf v1.5.2 // indirect
	github.com/kkdai/bstream v1.0.0 // indirect
	github.com/lightninglabs/neutrino v0.14.2 // indirect
	github.com/lightningnetwork/lnd/tlv v1.0.3 // indirect
	gitlab.com/yawning/bsaes.git v0.0.0-20190805113838-0a714cd429ec // indirect
	go.uber.org/atomic v1.7.0 // indirect
	golang.org/x/crypto v0.0.0-20210921155107-089bfa567519 // indirect
	golang.org/x/net v0.0.0-20211015210444-4f30a5c0130f // indirect
	golang.org/x/sys v0.0.0-20220722155257-8c9f86f7a55f // indirect
	golang.org/x/text v0.3.7 // indirect
	google.golang.org/genproto v0.0.0-20210617175327-b9e0b3197ced // indirect
)

replace github.com/niftynei/glightning v0.8.2 => github.com/breez/glightning v0.0.0-20221201090905-688d312b61f4

module github.com/tendermint/tendermint

go 1.16

require (
	github.com/BurntSushi/toml v1.0.0
	github.com/Workiva/go-datastructures v1.0.53
	github.com/adlio/schema v1.2.3
	github.com/btcsuite/btcd v0.22.0-beta
	github.com/btcsuite/btcutil v1.0.3-0.20201208143702-a53e38424cce
	github.com/dashevo/dashd-go v0.0.0-20210630125816-b417ad8eb165
	github.com/dashpay/bls-signatures/go-bindings v0.0.0-20201127091120-745324b80143
	github.com/fortytw2/leaktest v1.3.0
	github.com/go-kit/kit v0.12.0
	github.com/go-pkgz/jrpc v0.2.0
	github.com/gogo/protobuf v1.3.2
	github.com/golang/protobuf v1.5.2
	github.com/golangci/golangci-lint v1.43.0
	github.com/google/orderedcode v0.0.1
	github.com/google/uuid v1.3.0
	github.com/gorilla/websocket v1.5.0
	github.com/grpc-ecosystem/go-grpc-middleware v1.3.0
	github.com/grpc-ecosystem/go-grpc-prometheus v1.2.0
	github.com/hashicorp/go-multierror v1.1.1
	github.com/lib/pq v1.10.4
	github.com/libp2p/go-buffer-pool v0.0.2
	github.com/minio/highwayhash v1.0.2
	github.com/mroth/weightedrand v0.4.1
	github.com/oasisprotocol/curve25519-voi v0.0.0-20210609091139-0a56a4bca00b
	github.com/ory/dockertest v3.3.5+incompatible
	github.com/prometheus/client_golang v1.12.0
	github.com/rcrowley/go-metrics v0.0.0-20201227073835-cf1acfcdf475
	github.com/rs/cors v1.8.2
	github.com/rs/zerolog v1.26.1
	github.com/sasha-s/go-deadlock v0.3.1
	github.com/snikch/goodman v0.0.0-20171125024755-10e37e294daa
	github.com/spf13/cobra v1.2.1
	github.com/spf13/viper v1.9.0
	github.com/stretchr/testify v1.7.0
	github.com/tendermint/tm-db v0.6.4
	github.com/vektra/mockery/v2 v2.9.4
	golang.org/x/crypto v0.0.0-20211215165025-cf75a172585e
	golang.org/x/net v0.0.0-20211208012354-db4efeb81f4b
	golang.org/x/sync v0.0.0-20210220032951-036812b2e83c
	google.golang.org/grpc v1.41.0
	gopkg.in/check.v1 v1.0.0-20200902074654-038fdea0a05b // indirect
	pgregory.net/rapid v0.4.7
)

replace github.com/tendermint/tendermint => ./

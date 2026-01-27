---
order: 3
---

# Install Tenderdash

## Using Dashmate (Recommended Method)

Dashmate is a part of the Dash Platform and provides a comprehensive solution for installing the entire platform. We highly recommend using Dashmate for a seamless and straightforward installation process.

For detailed instructions on how to set up a node using Dashmate, please refer to the official Dash documentation: [Set Up a Node](https://docs.dash.org/en/stable/docs/user/network/dashmate/index.html).

## From Binary

To download pre-built binaries, see the [releases page](https://github.com/dashevo/tenderdash/releases).

## From Source

Install official go into the standard location with standard PATH updates:

  ```bash
  curl https://webinstall.dev/go | sh
  source ~/.config/envman/PATH.env
  ```

Install git, cmake, and build-essential (apt) or build-base (apk) and other libs.

For Debian-based (eg. Ubuntu):
  
  ```bash
  sudo apt update
  sudo apt install -y git build-essential cmake libgmp-dev
  ```

For Alpine Linux:

  ```bash
  apk add --no-cache git build-base cmake gmp-dev gmp-static
  ```

Get the source code:

  ```bash
  git clone https://github.com/dashpay/tenderdash.git
  pushd ./tenderdash/
  git submodule init
  git submodule update
  ```

Build:

to put the binary in `$GOPATH/bin`:

  ```sh
  make install
  ```

to put the binary in `./build`:

  ```sh
  make build
  ```

The latest Tenderdash is now installed. You can verify the installation by
running:

```sh
tendermint version
```


### Cross-compilation

To cross-compile for ARM platform, you need to install required compilers. On Ubuntu 20.04+:

```bash
sudo apt-get install gcc-10-arm-linux-gnueabi g++-10-arm-linux-gnueabi
```

To start the build process, execute:

```bash
GOOS=linux GOARCH=arm  make
```

## Run

To start a one-node blockchain with a simple in-process application:

```sh
tendermint init validator
tendermint start --proxy-app=kvstore
```

## Reinstall

If you already have Tenderdash installed, and you make updates, simply

```sh
make install
```

To upgrade, run

```sh
git pull origin master
make install
```

## Compile with CLevelDB support

Install [LevelDB](https://github.com/google/leveldb) (minimum version is 1.7).

Install LevelDB with snappy (optionally). Below are commands for Ubuntu:

```sh
sudo apt-get update
sudo apt install build-essential

sudo apt-get install libsnappy-dev

wget https://github.com/google/leveldb/archive/v1.20.tar.gz && \
  tar -zxvf v1.20.tar.gz && \
  cd leveldb-1.20/ && \
  make && \
  sudo cp -r out-static/lib* out-shared/lib* /usr/local/lib/ && \
  cd include/ && \
  sudo cp -r leveldb /usr/local/include/ && \
  sudo ldconfig && \
  rm -f v1.20.tar.gz
```

Set a database backend to `cleveldb`:

```toml
# config/config.toml
db_backend = "cleveldb"
```

To install Tenderdash, run:

```sh
CGO_LDFLAGS="-lsnappy" make install TENDERMINT_BUILD_OPTIONS=cleveldb
```

or run:

```sh
CGO_LDFLAGS="-lsnappy" make build TENDERMINT_BUILD_OPTIONS=cleveldb
```

which puts the binary in `./build`.

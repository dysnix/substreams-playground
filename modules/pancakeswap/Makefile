ENDPOINT ?= bsc.streamingfast.io:443
GRAPH_CONFIG ?= ../graph-node-dev/config/graphman.toml

.PHONY: build-and-pack-eth-token
build-and-pack-eth-token:
	cd ../eth-token && cargo build --target wasm32-unknown-unknown --release && substreams pack -o eth-token-at-pcs.spkg substreams.yaml

.PHONY: build
build: build-and-pack-eth-token
	cargo build --target wasm32-unknown-unknown --release

.PHONY: stream
stream: build
	substreams run -e $(ENDPOINT) substreams.yaml map_pairs -s 6810706 -t +10000 --production-mode

.PHONY: codegen
codegen:
	substreams protogen ./substreams.yaml --exclude-paths="sf/ethereum,sf/substreams,google"

.PHONE: package
package: build
	substreams pack -o substreams.spkg substreams.yaml

.PHONE: deploy_local
deploy_local: package
	mkdir build 2> /dev/null || true
	graph build --ipfs https://api.thegraph.com/ipfs/ subgraph.yaml
	graph create pcs --node http://127.0.0.1:8020
	graph deploy --node http://127.0.0.1:8020 --ipfs https://api.thegraph.com/ipfs/ --version-label v0.0.1 pcs subgraph.yaml

.PHONE: undeploy_local
undeploy_local:
	graphman --config "$(GRAPH_CONFIG)" drop --force pancakeswap_v3
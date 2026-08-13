.PHONY: generate test lint build check clab clab-prereq clab-pki clab-image clab-render clab-deploy clab-ready clab-status clab-destroy

CONTAINERLAB ?= containerlab
CLAB_TOPO := clab/bgpls.clab.yml
CLAB_IMAGE ?= bgpls:local

generate:
	buf generate

test:
	go test -buildvcs=false ./...

lint:
	buf lint

build:
	go build -buildvcs=false -o bgpls ./cmd/bgpls

check: lint test build

clab-prereq:
	@chmod +x clab/scripts/*.sh clab/scripts/*.py clab/collector/entrypoint.sh
	@command -v docker >/dev/null || { echo "docker is required on the Containerlab host"; exit 1; }
	@command -v $(CONTAINERLAB) >/dev/null || { echo "containerlab is required. Install it from https://containerlab.dev/install/ or set CONTAINERLAB='sudo containerlab'"; exit 1; }
	@command -v openssl >/dev/null || { echo "openssl is required to mint the lab mTLS certificates"; exit 1; }
	@command -v python3 >/dev/null || { echo "python3 is required to render FRR configs"; exit 1; }
	@docker info >/dev/null 2>&1 || { echo "docker is not usable from this user. Add the user to the docker group or run with a working Docker context."; exit 1; }

clab-render:
	python3 clab/scripts/render-frr.py

clab-pki:
	bash clab/scripts/gen-pki.sh

clab-image:
	docker build -f clab/collector/Dockerfile -t $(CLAB_IMAGE) .
	@cid=$$(docker create --entrypoint /bin/true $(CLAB_IMAGE)); \
	docker cp $$cid:/usr/local/bin/bgpls ./bgpls; \
	docker rm $$cid >/dev/null; \
	chmod +x ./bgpls

clab-deploy: clab-prereq clab-render clab-pki clab-image
	rm -rf clab/data
	mkdir -p clab/data
	$(CONTAINERLAB) deploy -t $(CLAB_TOPO) --reconfigure

clab-ready:
	bash clab/scripts/wait-ready.sh

clab: clab-deploy clab-ready
	@host_ip=$$(cat clab/pki/host-ip 2>/dev/null || echo 127.0.0.1); \
	echo; \
	echo "bgPLS lab is up. UI: http://127.0.0.1:8080/ui/  API: https://127.0.0.1:7443  metrics: http://127.0.0.1:9090/metrics"; \
	echo "If you are on another host, open http://$${host_ip}:8080/ui/ or use https://$${host_ip}:7443 with the lab certificates in clab/pki."; \
	echo; \
	echo "Query topology:"; \
	echo "  ./clab/scripts/bgpls.sh topology summary"; \
	echo "  ./clab/scripts/bgpls.sh topology nodes --domain core"; \
	echo "  ./clab/scripts/bgpls.sh topology links --domain core"; \
	echo "  ./clab/scripts/bgpls.sh topology prefixes --domain core"; \
	echo "  ./clab/scripts/bgpls.sh path compute --domain core --source r1 --destination r8 --metric igp"; \
	echo "  ./clab/scripts/bgpls.sh peers list"; \
	echo; \
	echo "Raw CLI equivalent:"; \
	echo "  ./bgpls topology summary --server https://127.0.0.1:7443 --ca clab/pki/ca.crt --cert clab/pki/admin.crt --key clab/pki/admin.key"; \
	echo; \
	echo "Debug: make clab-status    Destroy: make clab-destroy"

clab-status:
	bash clab/scripts/status.sh

clab-destroy:
	-$(CONTAINERLAB) destroy -t $(CLAB_TOPO) --cleanup
	rm -rf clab/data

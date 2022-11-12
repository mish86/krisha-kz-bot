PROJECTNAME	= krisha-kz-bot

SHELL := /bin/bash

GOBASE		= $(shell pwd)
GOBIN		= $(GOBASE)/bin
GOWEB		= ${GOBASE}/cmd/web/web.go
GOWORKER	= ${GOBASE}/cmd/worker/worker.go
GOPKG		= ${GOBASE}/pkg

HEROKU_REG	= registry.heroku.com
TAG   		= $(shell git log -1 --pretty=%h)
IMG    		= $(HEROKU_REG)/${PROJECTNAME}/web:${TAG}
LATEST 		= $(HEROKU_REG)/${PROJECTNAME}/web:latest

# HEROKU_PG_DB_NAME = $(PROJECTNAME)-pg
HEROKU_REDIS_DB_NAME = $(PROJECTNAME)-redis

PODMAN_STATE = $(shell podman info > /dev/null 2>&1; echo $$?)
DOCKER_STATE = $(shell docker info > /dev/null 2>&1; echo $$?)

## build: Build binary files.
build: clean
# Target
	go build -race -ldflags "-extldflags '-static'" -o $(GOBIN)/web ${GOWEB}
	go build -race -ldflags "-extldflags '-static'" -o $(GOBIN)/worker ${GOWORKER}
# MacOS
#	CGO_ENABLED=0 GOARCH="amd64" GOOS="darwin" go build -o $(GOBIN)/web ${GOWEB}
#	CGO_ENABLED=0 GOARCH="amd64" GOOS="darwin" go build -o $(GOBIN)/worker ${GOWORKER}
# Heroku
#	CGO_ENABLED=0 GOARCH="amd64" GOOS="linux" go build -o $(GOBIN)/web ${GOWEB}
#	CGO_ENABLED=0 GOARCH="amd64" GOOS="linux" go build -o $(GOBIN)/worker ${GOWORKER}

## run: Run worker
run:
	$(eval -include .env)
	$(eval export)
	env
	go run ${GOWORKER}

## clean: Clean build files.
clean:
	go clean
	rm -rf ${GOBIN}

## dep: Downloads modules dependencies.
dep:
	go mod tidy -v
	go mod download -x

## lint: Runs `golangci-lint` internally.
lint:
	golangci-lint run

## test: Runs tests.
test:
	go fmt $(shell go list ./... | grep -v /vendor/)
	go vet $(shell go list ./... | grep -v /vendor/)
	go test -race $(shell go list ./... | grep -v /vendor/)

test-json:
	go test -race $(shell go list ./... | grep -v /vendor/) -json > ./test-results.json

# ## pg-start: Starts postgres daemon.
# pg-start:
# 	@pg_ctl -D /usr/local/var/postgres -l /usr/local/var/postgres/server.log -p /usr/local/bin/postgres start

# ## pg-stop: Stops postgres daemon.
# pg-stop:
# 	@-pg_ctl -D /usr/local/var/postgres stop

# ## pg-migrate: Run migration in postgres (local).
# pg-migrate: pg-start
# 	@psql -h localhost -p 5432 -U postgres -f ./init/pg/migrate.local.sql

## image-build: Build image using podman or docker
image-build:
ifeq ($(PODMAN_STATE),0)
	podman --log-level=info build --file Dockerfile --tag $(IMG) .
	podman --log-level=info tag ${IMG} ${LATEST}
else ifeq ($(DOCKER_STATE),0)
	docker build --file Dockerfile --tag $(IMG) .
	docker tag ${IMG} ${LATEST}
else
	@echo "podman machine or docker daemon is not running"
	@exit 1
endif

## reg-login: Log in in heroku container registry
reg-login:
ifeq ($(PODMAN_STATE),0)
	@$(eval HEROKU_TOKEN := $(if $(HEROKU_TOKEN),$(HEROKU_TOKEN),$(shell heroku auth:token)))
	@podman --log-level=info login --username=_ --password=$(HEROKU_TOKEN) $(HEROKU_REG)
else ifeq ($(DOCKER_STATE),0)
	@$(eval HEROKU_TOKEN := $(if $(HEROKU_TOKEN),$(HEROKU_TOKEN),$(shell heroku auth:token)))
	@docker login --username=_ --password=$(HEROKU_TOKEN) $(HEROKU_REG)
else
	@echo "podman machine or docker daemon is not running"
	@exit 1
endif	

## reg-logout: Log out in heroku container registry
reg-logout:
ifeq ($(PODMAN_STATE),0)
	@-podman --log-level=info logout $(HEROKU_REG)
else ifeq ($(DOCKER_STATE),0)
	@-docker logout $(HEROKU_REG)
else
	@echo "podman machine or docker daemon is not running"
	@exit 1
endif	

## image-push: Upload last image built localy into heroku container registry
image-push: image-id
ifeq ($(PODMAN_STATE),0)
	podman --log-level=debug push --format=v2s2 $(IMG)
else ifeq ($(DOCKER_STATE),0)
	docker push $(IMG)
else
	@echo "podman machine or docker daemon is not running"
	@exit 1
endif

## image-id: Get image id built localy
image-id:
ifeq ($(PODMAN_STATE),0)
	@$(eval IMG_ID := "sha256:$(shell podman inspect $(IMG) --format={{.Id}})")
	@echo "  >  Image ID is ${IMG_ID}"
else ifeq ($(DOCKER_STATE),0)
	@$(eval IMG_ID := $(shell docker inspect $(IMG) --format={{.Id}}))
	@echo "  >  Image ID is ${IMG_ID}"
else
	@echo "podman machine or docker daemon is not running"
	@exit 1
endif

image-id-silent:
ifeq ($(PODMAN_STATE),0)
	@$(eval IMG_ID := "sha256:$(shell podman inspect $(IMG) --format={{.Id}})")
	@echo ${IMG_ID}
else ifeq ($(DOCKER_STATE),0)
	@$(eval IMG_ID := $(shell docker inspect $(IMG) --format={{.Id}}))
	@echo ${IMG_ID}
else
	@echo "podman machine or docker daemon is not running"
	@exit 1
endif	

## image-release: Release image uploaded in heroky container registry and restart worker dyno
image-release: image-id
	@$(eval HEROKU_TOKEN := $(if $(HEROKU_TOKEN),$(HEROKU_TOKEN),$(shell heroku auth:token)))
	@echo "  >  Releasing $(IMG_ID)"
	@$(eval payload := {\"updates\":[{\"type\":\"worker\",\"docker_image\":\"$(IMG_ID)\"}]})
	@echo "  >  Payload $(payload)"
	curl -v --netrc -X PATCH https://api.heroku.com/apps/$(PROJECTNAME)/formation \
		-d "$(payload)" \
		-H "Content-Type: application/json" \
		-H "Accept: application/vnd.heroku+json; version=3.docker-releases" \
		-H "Authorization: Bearer ${HEROKU_TOKEN}"

heroku-create:
	heroku create $(PROJECTNAME) --region eu

# heroku-create-pg:
#	heroku addons:create --app $(PROJECTNAME) --name=$(HEROKU_PG_DB_NAME) --wait heroku-postgresql:hobby-dev

heroku-create-redis:
	heroku addons:create --app $(PROJECTNAME) --name=$(HEROKU_REDIS_DB_NAME) --wait heroku-redis:hobby-dev

## heroku-config: Parse .env file and set parameters in heroku envs for the application
heroku-config:
	@cat .env | xargs -I PARAM sh -c "heroku config:set PARAM --app $(PROJECTNAME)"
	heroku config --app $(PROJECTNAME)

# heroku-push:
#	heroku container:push --app $(PROJECTNAME) web -v

#heroku-release:
#	heroku container:release --app $(PROJECTNAME) web -v

heroku-local: build
	heroku local

#heroku-scale:
#	heroku ps:scale --app $(PROJECTNAME) $(if $(web),web=$(web),)

#heroku-logs-web:
#	heroku logs --num=100 --tail --force-colors --app $(PROJECTNAME) --dyno=web

## heroku-logs-worker: output heroku logs for worker dyno
heroku-logs-worker:
	heroku logs --num=100 --tail --force-colors --app $(PROJECTNAME) --dyno=worker

heroku-restart-worker:
	heroku dyno:restart --app $(PROJECTNAME) --dyno=worker

heroku-redis-cli:
	heroku redis:cli --app $(PROJECTNAME)

# heroku-pg:
#	heroku pg --app $(PROJECTNAME)

# heroku-psql:
#	heroku pg:psql $(PROJECTNAME)-pg --app $(PROJECTNAME)


all: help
help: Makefile
	@echo
	@echo " Choose a command run in "$(PROJECTNAME)":"
	@echo
	@sed -n 's/^##//p' $< | column -t -s ':' |  sed -e 's/^/ /'
	@echo
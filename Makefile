SERVICE_NAME ?= slack-invite

# The names ooni/devops' ooniapi_service and ooniapi_service_deployer use
ECS_CONTAINER_NAME ?= ooniapi-service-$(SERVICE_NAME)
IMAGE_NAME ?= ooni/$(SERVICE_NAME)
DATE := $(shell date -u +%Y%m%d)
GIT_FULL_SHA ?= $(shell git rev-parse HEAD)
SHORT_SHA := $(shell echo ${GIT_FULL_SHA} | cut -c1-8)
BUILD_LABEL := $(DATE)-$(SHORT_SHA)
ENV_LABEL ?= latest

.PHONY: docker-build docker-smoketest docker-push

# Builds the image; the Go tests run as part of the build
docker-build:
	docker build --pull -t ${IMAGE_NAME}:${BUILD_LABEL} -t ${IMAGE_NAME}:${ENV_LABEL} .
	echo "built image: ${IMAGE_NAME}:${BUILD_LABEL} (${IMAGE_NAME}:${ENV_LABEL})"

docker-smoketest:
	./scripts/docker-smoketest.sh ${IMAGE_NAME}:${BUILD_LABEL}

docker-push:
	docker push ${IMAGE_NAME}:${BUILD_LABEL}
	docker push ${IMAGE_NAME}:${ENV_LABEL}

imagedefinitions.json:
	echo '[{"name":"${ECS_CONTAINER_NAME}","imageUri":"${IMAGE_NAME}:${BUILD_LABEL}"}]' > imagedefinitions.json

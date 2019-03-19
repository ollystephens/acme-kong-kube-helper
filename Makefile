NAME=acme-kong-kube-helper
VERS=0.0.1

REPO=ollystephens/kubernetes

.PHONY: build docker publish
.DEFAULT: build

build:
	go build .

docker:
	docker build -t ${NAME}:${VERS} .

publish: docker
	docker tag ${NAME}:${VERS} ${REPO}/${NAME}:${VERS}
	docker push ${REPO}/${NAME}:${VERS}

export KUBECONFIG=$(HOME)/.kube/dev
image=paskalmaksim/gitlab-token-rotate:$(shell git rev-parse --short HEAD)

build:
	docker build --platform=linux/amd64,linux/arm64 --pull --push -t $(image) .

deploy:
	helm upgrade gitlab-token-rotate ./charts/gitlab-token-rotate \
	--install \
	--namespace=gitlab-token-rotate \
	--create-namespace

test:
	go mod tidy
	go run github.com/golangci/golangci-lint/cmd/golangci-lint@latest run -v
	go vet ./...
	go test ./...

run:
	go run ./main.go -debug

add-examples:
	kubectl apply -f ./example

clean:
	kubectl delete -f ./example

lint:
	ct lint --all
.PHONY: build run deploy

build:
	sam build
run:
	sam build
	sam local invoke FetchDataFunction -e events/event.json
deploy:
	sam build
	sam deploy
test:
	sam validate --lint

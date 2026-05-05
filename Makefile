.PHONY: run build clean

run:
	go run main.go &

build:
	go build -o ios-build-server main.go
	./ios-build-server &

clean:
	rm -f ios-build-server
	rm -rf builds/ uploads/

tidy:
	go mod tidy
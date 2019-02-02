.PHONY: install_godog
install_godog:
	go get github.com/DATA-DOG/godog/cmd/godog

.PHONY: test_e2e
test_e2e: install_godog
	godog

.PHONY: test_unit
test_unit:
	go test

.PHONY: test
test: test_unit test_e2e

package main_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestNetter(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Netter Suite")
}

// netter_integration_test.go
package main_test

import (
	"fmt"
	"io"
	"net"
	"os/exec"
	"time"

	"github.com/containernetworking/cni/pkg/skel"
	"github.com/containernetworking/plugins/pkg/ns"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gexec"
)

const cniConfig = `{
	"cniVersion": "1.0.0",
	"name": "netter",
	"type": "netter-cni",
	"ipam": {
		"type": "host-local",
		"subnet": "10.10.1.0/24"
	}
}`

var cniPath string

var _ = BeforeSuite(func() {
	// Compile the CNI binary before running tests
	var err error
	cniPath, err = gexec.Build("..") // Assumes tests are in a sub-directory
	Expect(err).NotTo(HaveOccurred())
})

var _ = AfterSuite(func() {
	gexec.CleanupBuildArtifacts()
})

var _ = Describe("Netter CNI Core Functionality", func() {
	It("should allow two pods on the same node to communicate", func() {
		// 1. Create network namespaces for a "server" and "client" pod
		serverNS, err := ns.NewNS()
		Expect(err).NotTo(HaveOccurred())
		defer serverNS.Close()

		clientNS, err := ns.NewNS()
		Expect(err).NotTo(HaveOccurred())
		defer clientNS.Close()

		// 2. Call the CNI ADD command for the server
		cmd := exec.Command(cniPath)
		cmd.Env = []string{
			"CNI_COMMAND=ADD",
			"CNI_CONTAINERID=server-pod",
			"CNI_NETNS=" + serverNS.Path(),
			"CNI_IFNAME=eth0",
			"CNI_PATH=" + cniPath,
		}
		stdin, _ := cmd.StdinPipe()
		io.WriteString(stdin, cniConfig)
		stdin.Close()
		output, err := cmd.CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), string(output))

		// 3. Call the CNI ADD command for the client
		cmd = exec.Command(cniPath)
		cmd.Env = []string{
			"CNI_COMMAND=ADD",
			"CNI_CONTAINERID=client-pod",
			"CNI_NETNS=" + clientNS.Path(),
			"CNI_IFNAME=eth0",
			"CNI_PATH=" + cniPath,
		}
		stdin, _ = cmd.StdinPipe()
		io.WriteString(stdin, cniConfig)
		stdin.Close()
		output, err = cmd.CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), string(output))

		// 4. Run the TCP echo test
		By("starting an echo server in the server namespace")
		errCh := make(chan error, 1)
		go serverNS.Do(func(_ ns.NetNS) error {
			listener, err := net.Listen("tcp", "10.10.1.2:8080") // First IP from subnet
			if err != nil {
				errCh <- fmt.Errorf("failed to listen: %w", err)
				return err
			}
			defer listener.Close()
			errCh <- nil // Signal that we are listening

			conn, err := listener.Accept()
			if err != nil {
				return err
			}
			defer conn.Close()
			io.Copy(conn, conn)
			return nil
		})
		// Wait for the server to start listening
		Eventually(errCh).Should(Receive(BeNil()))

		By("connecting from the client namespace")
		clientNS.Do(func(_ ns.NetNS) error {
			time.Sleep(100 * time.Millisecond) // Give server a moment
			conn, err := net.Dial("tcp", "10.10.1.2:8080")
			Expect(err).NotTo(HaveOccurred())
			defer conn.Close()

			msg := "hello netter"
			_, err = conn.Write([]byte(msg))
			Expect(err).NotTo(HaveOccurred())

			resp := make([]byte, len(msg))
			_, err = conn.Read(resp)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(resp)).To(Equal(msg))
		})
	})
})

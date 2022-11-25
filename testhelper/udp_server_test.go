package testhelper_test

import (
	"bytes"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/Shopify/toxiproxy/v2/testhelper"
)

const bigString2k = "7f188ae8b8bdca4329ceb5526d0735d5a2312afecf6f0bd9302c2dc25ec55f1b9a638e3f1e3c6e12e68fe672e019335aabc8c58e" +
	"50550e8f079cf5e0061d6ea60c3a82ff85afe903915f207f3dacaec659069bc806142938541d3483b1b9a3fdbe98826a02711654a84eaa61183f2091" +
	"461753e75fecac00687da0d2f302930a5535b1ea31147770b241d678e85cc832178588fab0bcb67b7380c67e13a292e074537d37be0d9fd92ac76666" +
	"925fa35914bb0f7b7128ee3d90a2b2f0459c4845ad7e80734fe3814a9022328dc0f04044986d9bca202a77ca41d8c37812b01e1827c8ccbd070d4e32" +
	"50907d38062dac905eca928f8708cb55b7953b78e62a83f22876835c4340a91bb77e03c240712a79a6a9b900fdb906e1eded810badf07b0a7a5cd4a9" +
	"91a135fe07631ce664ebb21aa680cb46544accd55a76c1828076b43b33619e7e16da3bcf759dbad150632aac0d70453d8f784aa27f7c9eb680ade9dd" +
	"1a8497e7318fa75aa2056987345aa94a384031f179bec67d188281461f7d564e2e19463c4958b021077da794a678967b6b7b5264b35c053a51d9b2da" +
	"a3742967d1c3d53c08d74edd231131dc217fb61305d2208899779354b96d86cdbf1f072e80e8ff2b8a00740b2a79a7f641698bc40adeefea537543d8" +
	"aec76dce6ee9e33307fcdeb0791934bc90f7a318705c6a54d03c86be1abd3f8fc88de1d9216e88e50b017028a8ffbb6eacac740ddbe2552e1222b65f" +
	"2ed460fe680518619515b7279e193801654e36aa90538ebedf0bce9766964220239f92503fac71714ca2be2b85efe28b8589bef0d4e9171dfc0c6637" +
	"9f22b9949e038686d65cff9de3624d694faf1fa156cc55186f805d3295a13eac8e3bcfbd1dcdbd9d15ddbe81c0571e61990ce8b6af0d5677ce31868d" +
	"3112f14bd6a93f8702c7d04d39f7f6245c0af1946136d2029854de2679e6172cbfc7a1bbb966b3e05f84ce71ed0c5cb80bd7ba9dd50ff5d1f7249e2e" +
	"2e9b311693d3a2a74b7ec3b3fe462dba71bfb29e6b50427fd09c1ba13e6bd7c79116e85366c8031c175166de2c95489b1f476d0b90f9bad69e1c3ebd" +
	"017feb973f1bfef8e675c1ea368d30ee11cae833d6987012e37daf51f5d1d75795c6eaa4ea8cac7cf6b983d3502f4df32da32cc2ffea53583fbe6dcc" +
	"c1aee96092d913c0a3c17630fa4d78a1bae4ac6e8ba8ef70c3fdcba3cd1241bccfa6988526760145990f00e8d3a42d8ff0ff3d1a344783bb5ce3fb12" +
	"9954668ff4c9716a7ae42f43ffcffc6d0a1a7f987a12b7a50b64ac8fcbef1694cd6c7b157a633efbc620d1c37804c268dd8d7f602d9c3a69a85e64fb" +
	"f84e53521c81b25cd17effca689811d60e4c565a48da100f6574f780b15a762835671dcef63158271719fc413daeb98e"

func TestSimpleUdpServer(t *testing.T) {
	testhelper.WithUDPServer(t, func(addr string) {
		raddr, err := net.ResolveUDPAddr("udp", addr)
		if err != nil {
			t.Fatal("Failed to resolve server udp addr:", err)
		}

		conn, err := net.DialUDP("udp", nil, raddr)
		if err != nil {
			t.Fatal("Failed to dial udp server:", err)
		}
		defer conn.Close()

		msg := []byte("hello world")

		_, err = conn.Write(msg)
		if err != nil {
			t.Fatal("Failed writing to UDP server", err)
		}

		conn.SetReadDeadline(time.Now().Add(time.Second))
		buf := make([]byte, 1024)
		n, err := conn.Read(buf)
		if !bytes.Equal(buf[:n], msg) {
			t.Fatalf("Server didn't response same bytes from client; err: %v, request: %s, response: %s", err, msg, buf[:n])
		}

	})
}

func TestSimpleUdpServerBig(t *testing.T) {
	testhelper.WithUDPServer(t, func(addr string) {
		raddr, err := net.ResolveUDPAddr("udp", addr)
		if err != nil {
			t.Error("Failed to resolve server udp addr:", err)
		}

		conn, err := net.DialUDP("udp", nil, raddr)
		if err != nil {
			t.Error("Failed to dial udp server:", err)
		}
		defer conn.Close()

		msg := []byte(bigString2k)

		_, err = conn.Write(msg)
		if err != nil {
			t.Error("Failed writing to TCP server", err)
		}

		conn.SetReadDeadline(time.Now().Add(time.Second))
		buf := make([]byte, 1024)
		n, err := conn.Read(buf)
		if err != nil {
			t.Error("Unexpected error reading from udp server:", err)
		}
		// First 1024 bytes of message is read
		if !bytes.Equal(buf[:n], msg[:len(buf)]) {
			t.Errorf("Server didn't response same bytes from client, request: %s, response: %s", msg, buf[:n])
		}
		_, err = conn.Read(buf)
		// If message is bigger than read buffer, the rest of message is dropped
		// Second call to Read timeouts
		if err == nil || !strings.Contains(err.Error(), "timeout") {
			t.Error("Unexpected error state waiting for read second time from udp server:", err)
		}
	})
}

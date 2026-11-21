package extract

import (
	"os"
	"testing"

	"jsclayton.dev/wishbone/internal/netguard/netguardtest"
)

// No test in this package may ask the machine's DNS.
func TestMain(m *testing.M) { os.Exit(netguardtest.RunWithoutSystemDNS(m)) }

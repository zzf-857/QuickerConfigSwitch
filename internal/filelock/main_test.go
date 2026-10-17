package filelock

import (
	"testing"

	"github.com/zzf-857/quickerconfigswitch/internal/testenv"
)

// The tests run in a home of their own, never the user's real files.
func TestMain(m *testing.M) { testenv.Main(m) }

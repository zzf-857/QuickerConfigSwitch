//go:build !nogui

package gui

import (
	"fmt"
	"log"
)

func logf(format string, a ...any) { log.Print("qcs: " + fmt.Sprintf(format, a...)) }

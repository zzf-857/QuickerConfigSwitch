package cli

import (
	"flag"
	"fmt"
	"strconv"

	"github.com/zzf-857/quickerconfigswitch/internal/importer"
	"github.com/zzf-857/quickerconfigswitch/internal/provider"
)

// scanImports and addImport are the importer, replaceable in tests.
var (
	scanImports = importer.Scan
	addImport   = importer.Add
)

func (e *env) cmdImport(args []string) error {
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	add := fs.String("add", "", "添加第 N 个候选, 或 all (所有尚不存在的)")
	id := fs.String("id", "", "新 provider 的 id (只与 --add N 合用)")
	pos, err := e.parse(fs, "qcs import [--add N|all] [--id ID]", args)
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		return usagef("import 不接受参数")
	}
	if *id != "" && (*add == "" || *add == "all") {
		return usagef("--id 只能与 --add N 合用")
	}
	which := -1 // index to add; -2 for all
	switch *add {
	case "":
	case "all":
		which = -2
	default:
		n, err := strconv.Atoi(*add)
		if err != nil || n < 1 {
			return usagef("--add 需要序号 (从 1 起) 或 all: %q", *add)
		}
		which = n - 1
	}

	cands, errs := scanImports()
	for _, err := range errs {
		fmt.Fprintf(e.err, "警告: %v\n", err)
	}
	if len(cands) == 0 {
		fmt.Fprintln(e.out, "没有发现可导入的 provider。")
		if which != -1 {
			return errSilent
		}
		return nil
	}
	if which == -1 {
		for i, c := range cands {
			p := c.Provider
			fmt.Fprintf(e.out, "%d. %s  (%s: %s)\n", i+1, p.ID, c.Source, c.Origin)
			key := provider.MaskKey(p.APIKey)
			if key == "" {
				key = "(none)"
			}
			fmt.Fprintf(e.out, "   key:    %s\n", key)
			fmt.Fprintf(e.out, "   base:   %s\n", p.BaseURL)
			fmt.Fprintf(e.out, "   apis:   %s\n", apisText(p.APIs))
			fmt.Fprintf(e.out, "   models: %d\n", len(p.Models))
			if c.Exists != "" {
				fmt.Fprintf(e.out, "   已存在: %s\n", c.Exists)
			}
			if c.Note != "" {
				fmt.Fprintf(e.out, "   note:   %s\n", c.Note)
			}
		}
		fmt.Fprintln(e.out, "用 qcs import --add N [--id ID] 或 --add all 添加。")
		return nil
	}

	if which == -2 {
		failed := 0
		for i, c := range cands {
			if c.Exists != "" {
				fmt.Fprintf(e.out, "跳过 %d. %s (已存在: %s)\n", i+1, c.Provider.ID, c.Exists)
				continue
			}
			p, err := addImport(c, "")
			if err != nil {
				failed++
				fmt.Fprintf(e.out, "失败 %d. %s: %v\n", i+1, c.Provider.ID, err)
				continue
			}
			fmt.Fprintf(e.out, "已添加 provider %s\n", p.ID)
		}
		if failed > 0 {
			return errSilent
		}
		return nil
	}
	if which >= len(cands) {
		return usagef("没有第 %d 个候选 (共 %d 个)", which+1, len(cands))
	}
	p, err := addImport(cands[which], *id)
	if err != nil {
		return err
	}
	fmt.Fprintf(e.out, "已添加 provider %s\n", p.ID)
	return nil
}

package cli

import (
	"context"
	"flag"
	"fmt"
	"strconv"
	"strings"

	"github.com/zzf-857/quickerconfigswitch/internal/provider"
)

func (e *env) cmdProvider(args []string) error {
	if len(args) == 0 {
		return usagef("provider 需要子命令: ls, add, rm, models, templates")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "ls":
		return e.providerLs(rest)
	case "add":
		return e.providerAdd(rest)
	case "rm":
		return e.providerRm(rest)
	case "models":
		return e.providerModels(rest)
	case "templates":
		return e.providerTemplates(rest)
	case "-h", "--help", "help":
		fmt.Fprint(e.out, usageText)
		return nil
	}
	return usagef("未知的 provider 子命令 %q", sub)
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func apisText(a provider.APIs) string {
	var s []string
	if a.Responses {
		s = append(s, "responses")
	}
	if a.Chat {
		s = append(s, "chat")
	}
	if a.Anthropic != "" {
		s = append(s, "anthropic="+a.Anthropic)
	}
	return strings.Join(s, ",")
}

func (e *env) providerLs(args []string) error {
	fs := flag.NewFlagSet("provider ls", flag.ContinueOnError)
	pos, err := e.parse(fs, "qcs provider ls", args)
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		return usagef("provider ls 不接受参数")
	}
	ps, err := provider.List()
	if err != nil {
		return err
	}
	if len(ps) == 0 {
		fmt.Fprintln(e.out, "还没有 provider。用 qcs provider add 添加。")
		return nil
	}
	for _, p := range ps {
		fmt.Fprintf(e.out, "%s  (%s)\n", p.ID, p.Name)
		fmt.Fprintf(e.out, "  base:   %s\n", p.BaseURL)
		key := provider.MaskKey(p.APIKey)
		if key == "" {
			key = "(none)"
		}
		fmt.Fprintf(e.out, "  key:    %s\n", key)
		fmt.Fprintf(e.out, "  apis:   %s\n", apisText(p.APIs))
		fmt.Fprintf(e.out, "  models: %d\n", len(p.Models))
	}
	return nil
}

func (e *env) providerAdd(args []string) error {
	fs := flag.NewFlagSet("provider add", flag.ContinueOnError)
	base := fs.String("base", "", "OpenAI 兼容的 base URL")
	key := fs.String("key", "", "API key")
	name := fs.String("name", "", "显示名")
	tmpl := fs.String("template", "", "模板: cpa, openai-compatible, anthropic-compatible")
	anth := fs.String("anthropic", "", "Anthropic Messages base URL")
	resp := fs.Bool("responses", false, "支持 OpenAI Responses")
	chat := fs.Bool("chat", false, "支持 OpenAI Chat")
	var headers multi
	fs.Var(&headers, "header", "额外请求头 K=V (可重复)")
	pos, err := e.parse(fs, "qcs provider add <id> --base URL [flags]", args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usagef("provider add 需要且仅需要一个 <id>")
	}
	p := provider.Provider{ID: pos[0]}
	if *tmpl != "" {
		found := false
		for _, t := range provider.Templates() {
			if t.ID == *tmpl {
				p.Name, p.BaseURL, p.APIs = t.Name, t.BaseURL, t.APIs
				found = true
			}
		}
		if !found {
			return usagef("未知模板 %q (见 qcs provider templates)", *tmpl)
		}
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	if *base != "" {
		p.BaseURL = *base
	}
	if *name != "" {
		p.Name = *name
	}
	if p.Name == "" {
		p.Name = p.ID
	}
	if *anth != "" {
		p.APIs.Anthropic = *anth
	}
	if set["responses"] {
		p.APIs.Responses = *resp
	}
	if set["chat"] {
		p.APIs.Chat = *chat
	}
	if *tmpl == "" && !set["responses"] && !set["chat"] && *anth == "" {
		// nothing said: assume an OpenAI-compatible endpoint
		p.APIs.Responses, p.APIs.Chat = true, true
	}
	p.APIKey = *key
	for _, h := range headers {
		k, v, ok := strings.Cut(h, "=")
		if !ok || strings.TrimSpace(k) == "" {
			return usagef("--header 需要 K=V 形式: %q", h)
		}
		if p.Headers == nil {
			p.Headers = map[string]string{}
		}
		p.Headers[strings.TrimSpace(k)] = v
	}
	if err := provider.Add(p); err != nil {
		return err
	}
	fmt.Fprintf(e.out, "已添加 provider %s\n", p.ID)
	return nil
}

func (e *env) providerRm(args []string) error {
	fs := flag.NewFlagSet("provider rm", flag.ContinueOnError)
	pos, err := e.parse(fs, "qcs provider rm <id>", args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usagef("provider rm 需要一个 <id>")
	}
	if err := provider.Remove(pos[0]); err != nil {
		return err
	}
	fmt.Fprintf(e.out, "已删除 provider %s\n", pos[0])
	return nil
}

func (e *env) providerModels(args []string) error {
	fs := flag.NewFlagSet("provider models", flag.ContinueOnError)
	fetch := fs.Bool("fetch", false, "从上游拉取模型并合并")
	add := fs.String("add", "", "添加模型, 逗号分隔")
	rm := fs.String("rm", "", "删除模型, 逗号分隔")
	var fast multi
	fs.Var(&fast, "fast", "设置 Fast: model=true|false (可重复)")
	pos, err := e.parse(fs, "qcs provider models <id> [flags]", args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usagef("provider models 需要一个 <id>")
	}
	p, ok, err := provider.Get(pos[0])
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("provider %q 不存在", pos[0])
	}
	changed := false
	if *fetch {
		got, err := provider.FetchModels(context.Background(), p)
		if err != nil {
			return err
		}
		n := 0
		for _, m := range got {
			if _, have := p.Model(m.ID); !have {
				p.Models = append(p.Models, m)
				n++
			}
		}
		fmt.Fprintf(e.out, "拉取到 %d 个模型, 新增 %d 个\n", len(got), n)
		changed = true
	}
	for _, id := range splitList(*add) {
		if _, have := p.Model(id); have {
			continue
		}
		p.Models = append(p.Models, provider.Model{ID: id, Name: id, Fast: provider.DefaultFast(id)})
		changed = true
	}
	for _, id := range splitList(*rm) {
		i := -1
		for j, m := range p.Models {
			if m.ID == id {
				i = j
			}
		}
		if i < 0 {
			return fmt.Errorf("模型 %q 不在 %s 中", id, p.ID)
		}
		p.Models = append(p.Models[:i:i], p.Models[i+1:]...)
		changed = true
	}
	for _, f := range fast {
		id, v, ok := strings.Cut(f, "=")
		b, perr := strconv.ParseBool(v)
		if !ok || perr != nil {
			return usagef("--fast 需要 model=true|false: %q", f)
		}
		found := false
		for j := range p.Models {
			if p.Models[j].ID == id {
				p.Models[j].Fast = b
				found = true
			}
		}
		if !found {
			return fmt.Errorf("模型 %q 不在 %s 中", id, p.ID)
		}
		changed = true
	}
	if changed {
		if err := provider.Update(p); err != nil {
			return err
		}
	}
	if len(p.Models) == 0 {
		fmt.Fprintln(e.out, "没有模型。用 --fetch 或 --add 添加。")
		return nil
	}
	for _, m := range p.Models {
		fmt.Fprintf(e.out, "%s  fast=%s\n", m.ID, yesNo(m.Fast))
	}
	return nil
}

func splitList(s string) []string {
	var out []string
	for _, x := range strings.Split(s, ",") {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	return out
}

func (e *env) providerTemplates(args []string) error {
	fs := flag.NewFlagSet("provider templates", flag.ContinueOnError)
	if _, err := e.parse(fs, "qcs provider templates", args); err != nil {
		return err
	}
	for _, t := range provider.Templates() {
		fmt.Fprintf(e.out, "%s  %s\n", t.ID, t.Name)
		if t.BaseURL != "" {
			fmt.Fprintf(e.out, "  base: %s\n", t.BaseURL)
		}
		fmt.Fprintf(e.out, "  apis: %s\n", apisText(t.APIs))
	}
	return nil
}

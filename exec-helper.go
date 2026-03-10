package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// 这是一个“按次启动”的最小 exec-helper：
// - stdin:  一行 JSON {"cmd":[...], "timeout_sec":10}
// - stdout: 一行 JSON {"stdout":"", "stderr":"", "exit_code":0, "error":""}
//
// 当前版本（稳定版）：内部调用 /root/ivc_exec_client（可用环境变量覆盖）。
// 这样可以避免在某些板子上直接 mmap /dev/hivc0 触发 SIGBUS。

type request struct {
	Cmd        []string `json:"cmd"`
	TimeoutSec int      `json:"timeout_sec"`
}

type response struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exit_code"`
	Error    string `json:"error,omitempty"`
}

func main() {
	// 检查是否是流模式
	if len(os.Args) > 1 && os.Args[1] == "--stream" {
		runStreamMode()
		return
	}

	// 原来的同步模式
	req, err := readRequest(os.Stdin)
	if err != nil {
		writeResponse(response{ExitCode: -1, Error: err.Error()})
		return
	}

	ivcClient := os.Getenv("IVC_EXEC_CLIENT_PATH")
	if ivcClient == "" {
		ivcClient = "/root/ivc_exec_client"
	}

	timeout := time.Duration(req.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	// 由于 ivc_exec_client 接收的是“单字符串命令”（在 guest 里 popen 执行），
	// 我们把 cmd[] 拼成一个 shell-safe 的命令字符串。
	cmdStr := shellJoin(req.Cmd)
	if cmdStr == "" {
		writeResponse(response{ExitCode: -1, Error: "empty cmd"})
		return
	}

	stdout, stderr, runErr := runIVCClient(ivcClient, cmdStr, timeout)
	outText, exitCode := parseIVCExecClientStdout(stdout)

	resp := response{
		Stdout:   outText,
		Stderr:   strings.TrimSpace(stderr),
		ExitCode: exitCode,
	}
	if runErr != nil {
		resp.Error = runErr.Error()
		if resp.ExitCode == 0 {
			resp.ExitCode = -1
		}
	}

	writeResponse(resp)
}

func readRequest(r io.Reader) (*request, error) {
	br := bufio.NewReader(r)
	line, err := br.ReadBytes('\n')
	if err != nil && err != io.EOF {
		return nil, fmt.Errorf("read stdin: %w", err)
	}
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return nil, fmt.Errorf("empty stdin")
	}
	var req request
	if err := json.Unmarshal(line, &req); err != nil {
		return nil, fmt.Errorf("invalid json: %w", err)
	}
	return &req, nil
}

func writeResponse(resp response) {
	enc := json.NewEncoder(os.Stdout)
	_ = enc.Encode(resp)
}

func runIVCClient(path string, cmdStr string, timeout time.Duration) (stdout string, stderr string, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	c := exec.CommandContext(ctx, path, cmdStr)
	var outBuf, errBuf bytes.Buffer
	c.Stdout = &outBuf
	c.Stderr = &errBuf
	runErr := c.Run()

	if ctx.Err() != nil {
		return outBuf.String(), errBuf.String(), fmt.Errorf("timeout after %s", timeout)
	}
	if runErr != nil {
		return outBuf.String(), errBuf.String(), fmt.Errorf("ivc_exec_client failed: %w", runErr)
	}
	return outBuf.String(), errBuf.String(), nil
}

// 如果 guest 命令非零，ivc_exec_agent 会在输出前加：
// EXIT_CODE=<n>
var exitCodeRe = regexp.MustCompile(`(?m)^EXIT_CODE=(\d+)\s*$`)

func parseIVCExecClientStdout(raw string) (output string, exitCode int) {
	exitCode = 0

	// 先提取 --- 之间的内容（ivc_exec_client 打印的 Output block）
	lines := strings.Split(raw, "\n")
	inBlock := false
	var block []string
	for _, line := range lines {
		if strings.TrimSpace(line) == "---" {
			if inBlock {
				break
			}
			inBlock = true
			continue
		}
		if inBlock {
			block = append(block, line)
		}
	}
	candidate := strings.Join(block, "\n")

	// 再从 candidate 里提取 EXIT_CODE
	if m := exitCodeRe.FindStringSubmatch(candidate); len(m) == 2 {
		if n, err := strconv.Atoi(m[1]); err == nil {
			exitCode = n
			candidate = exitCodeRe.ReplaceAllString(candidate, "")
			candidate = strings.TrimLeft(candidate, "\n")
		}
	}
	return candidate, exitCode
}
// --- shellJoin: 把 argv 拼成一个尽量安全的单字符串 ---
// 目标：适配 guest 端 popen 执行（避免空格/引号引发解析问题）。
func shellJoin(argv []string) string {
	if len(argv) == 0 {
		return ""
	}
	parts := make([]string, 0, len(argv))
	for _, a := range argv {
		parts = append(parts, shellEscape(a))
	}
	return strings.Join(parts, " ")
}

func shellEscape(s string) string {
	if s == "" {
		return "''"
	}
	// 仅允许常见安全字符，不含空格/引号/重定向符等
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
			r == '_' || r == '-' || r == '.' || r == '/' || r == ':' {
			continue
		}
		// 其他字符走单引号包裹
		return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
	}
	return s
}

// runStreamMode 流模式：持续从 stdin 读命令，通过 IVC 执行，输出到 stdout
// 协议：
// - 第一行：JSON {"cmd":[...], "timeout_sec":0}（timeout_sec=0 表示无超时）
// - 后续：每行一个命令（直接字符串，不是 JSON）
// - stdout：直接输出命令结果（不是 JSON）
func runStreamMode() {
	ivcClient := os.Getenv("IVC_EXEC_CLIENT_PATH")
	if ivcClient == "" {
		ivcClient = "/root/ivc_exec_client"
	}

	// 第一行：读取初始命令（JSON）
	br := bufio.NewReader(os.Stdin)
	firstLine, err := br.ReadBytes('\n')
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to read initial command: %v\n", err)
		return
	}

	var initReq request
	if err := json.Unmarshal(bytes.TrimSpace(firstLine), &initReq); err != nil {
		fmt.Fprintf(os.Stderr, "invalid initial command JSON: %v\n", err)
		return
	}

	// 执行初始命令（通常是启动一个 shell，比如 /bin/sh）
	initCmdStr := shellJoin(initReq.Cmd)
	if initCmdStr != "" {
		stdout, _, _ := runIVCClient(ivcClient, initCmdStr, 30*time.Second)
		outText, _ := parseIVCExecClientStdout(stdout)
		os.Stdout.WriteString(outText)
	}

	// 后续：循环读取 stdin 的每一行，作为命令执行
	// 注意：这是简化版，真正的交互式需要更复杂的协议（比如支持 raw mode、信号等）
	for {
		line, err := br.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			fmt.Fprintf(os.Stderr, "read error: %v\n", err)
			break
		}

		cmdStr := strings.TrimSpace(string(line))
		if cmdStr == "" {
			continue
		}

		// 特殊命令：退出
		if cmdStr == "exit" || cmdStr == "quit" {
			break
		}

		// 通过 IVC 执行命令
		stdout, stderr, runErr := runIVCClient(ivcClient, cmdStr, 30*time.Second)
		outText, exitCode := parseIVCExecClientStdout(stdout)

		// 输出结果（直接写到 stdout，不是 JSON）
		if len(outText) > 0 {
			os.Stdout.WriteString(outText)
		}
		if len(stderr) > 0 {
			os.Stderr.WriteString(stderr)
		}
		if runErr != nil {
			fmt.Fprintf(os.Stderr, "exec error: %v\n", runErr)
		}
		if exitCode != 0 {
			// 可以在这里输出退出码，但为了兼容性，先不输出
		}
	}
}

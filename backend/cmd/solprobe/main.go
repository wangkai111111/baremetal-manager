package main

import (
	"fmt"
	"os"
	"os/exec"
	"time"
)

func main() {
	cfg := []string{"-I", "lanplus", "-H", "172.16.20.35", "-U", "root", "-E", "sol", "activate"}
	cmd := exec.Command("ipmitool", cfg...)
	cmd.Env = append(os.Environ(), "IPMI_PASSWORD=cpy.com")

	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()

	if err := cmd.Start(); err != nil {
		fmt.Println("start err:", err)
		os.Exit(1)
	}
	fmt.Println("PID:", cmd.Process.Pid)

	// stdout goroutine
	done := make(chan error)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := stdout.Read(buf)
			if n > 0 {
				fmt.Printf("stdout(%d): %q\n", n, buf[:min(n, 60)])
			}
			if err != nil {
				fmt.Println("stdout.Read err:", err)
				done <- err
				return
			}
		}
	}()

	// 模拟 WebSocket 读写：发回车，然后等
	time.Sleep(2 * time.Second)
	stdin.Write([]byte("\r\n"))
	fmt.Println("sent enter")
	time.Sleep(3 * time.Second)
	stdin.Write([]byte("\r\n"))
	fmt.Println("sent enter 2")

	// 等 cmd 退出
	cmdErr := cmd.Wait()
	fmt.Println("Wait err:", cmdErr)
	select {
	case e := <-done:
		fmt.Println("stdout goroutine exited with:", e)
	case <-time.After(1 * time.Second):
	}
}

func min(a, b int) int { if a < b { return a }; return b }

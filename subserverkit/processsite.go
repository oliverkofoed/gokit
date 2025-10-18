package subserverkit

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/oliverkofoed/gokit/sitekit/web"
)

// ProcessSite configuration for running a process and proxying to it
type ProcessSite struct {
	WorkingDir      string
	Command         string
	Host            string
	OutputPrefix    string // Prefix for each line of output (e.g., "[console] "). Empty string discards output.
	StartOnCreation bool
}

// Site returns the Site interface for the ProcessSite
func (p ProcessSite) Site() Site {
	// Ensure host has proper format
	host := p.Host
	if !strings.Contains(host, ":") {
		panic("host must include port (e.g., ':9500' or 'localhost:9500')")
	}

	// Add localhost if only port is specified
	if strings.HasPrefix(host, ":") {
		host = "localhost" + host
	}

	ps := &processSiteImpl{
		workingDir:   p.WorkingDir,
		command:      p.Command,
		host:         host,
		outputPrefix: p.OutputPrefix,
	}

	// Start immediately if requested
	if p.StartOnCreation {
		if err := ps.start(); err != nil {
			panic(fmt.Sprintf("failed to start process on creation: %v", err))
		}
	}

	return ps
}

// processSiteImpl runs a process and proxies HTTP requests to it
type processSiteImpl struct {
	workingDir   string
	command      string
	host         string
	outputPrefix string
	proxy        *httputil.ReverseProxy
	cmd          *exec.Cmd
	started      bool
	mu           sync.Mutex
	cancel       context.CancelFunc
}

// start initializes the process and proxy (called on first request or immediately)
func (p *processSiteImpl) start() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.started {
		return nil
	}

	// Parse the command
	parts := strings.Fields(p.command)
	if len(parts) == 0 {
		return fmt.Errorf("empty command")
	}

	// Start the process
	p.cmd = exec.Command("sh", "-c", p.command)
	p.cmd.Dir = p.workingDir
	p.cmd.Env = os.Environ()

	// Configure the process group to allow killing all child processes
	// Setpgid puts the process in its own process group
	// Setsid creates a new session, making it a session leader
	p.cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
		Pgid:    0,
	}

	// Configure output based on outputPrefix
	if p.outputPrefix != "" {
		// Create pipes for stdout and stderr
		stdout, err := p.cmd.StdoutPipe()
		if err != nil {
			return fmt.Errorf("failed to create stdout pipe: %w", err)
		}
		stderr, err := p.cmd.StderrPipe()
		if err != nil {
			return fmt.Errorf("failed to create stderr pipe: %w", err)
		}

		// Start the process
		if err := p.cmd.Start(); err != nil {
			return fmt.Errorf("failed to start process: %w", err)
		}

		// Start goroutines to read and prefix output
		go p.prefixOutput(stdout, os.Stdout)
		go p.prefixOutput(stderr, os.Stderr)
	} else {
		// Discard output if no prefix specified
		if err := p.cmd.Start(); err != nil {
			return fmt.Errorf("failed to start process: %w", err)
		}
	}

	// Start a goroutine to monitor the process and ensure cleanup
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	go p.monitorProcess(ctx)

	// Wait a moment for the server to start
	time.Sleep(1 * time.Second)

	// Create the reverse proxy
	target, err := url.Parse("https://" + p.host)
	if err != nil {
		return fmt.Errorf("failed to parse host URL: %w", err)
	}

	p.proxy = httputil.NewSingleHostReverseProxy(target)
	p.proxy.Transport = &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
		},
	}

	// Custom error handler to provide better error messages
	p.proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		http.Error(w, fmt.Sprintf("Proxy error: %v", err), http.StatusBadGateway)
	}

	p.started = true
	return nil
}

// monitorProcess waits for the process to exit and cleans up
func (p *processSiteImpl) monitorProcess(ctx context.Context) {
	if p.cmd == nil || p.cmd.Process == nil {
		return
	}

	// Wait for either the process to exit or context cancellation
	done := make(chan error, 1)
	go func() {
		done <- p.cmd.Wait()
	}()

	select {
	case <-ctx.Done():
		// Context cancelled (Stop was called), kill the process group
		p.killProcessGroup()
	case <-done:
		// Process exited on its own
	}
}

// killProcessGroup kills the entire process group
func (p *processSiteImpl) killProcessGroup() {
	if p.cmd == nil || p.cmd.Process == nil {
		return
	}

	// Get the process group ID
	pgid, err := syscall.Getpgid(p.cmd.Process.Pid)
	if err == nil {
		// Kill the entire process group (negative PID kills the process group)
		syscall.Kill(-pgid, syscall.SIGKILL)
	} else {
		// Fallback: kill just the main process
		p.cmd.Process.Kill()
	}
}

// prefixOutput reads from a reader line by line and writes to a writer with a prefix
func (p *processSiteImpl) prefixOutput(reader io.Reader, writer io.Writer) {
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := scanner.Text()
		fmt.Fprintf(writer, "%s%s\n", p.outputPrefix, line)
	}
}

// ServeHTTP handles HTTP requests by proxying to the running process
func (p *processSiteImpl) ServeHTTP(c *web.Context) {
	// Start the process on first request
	if !p.started {
		if err := p.start(); err != nil {
			http.Error(c, fmt.Sprintf("Failed to start development server: %v", err), http.StatusInternalServerError)
			return
		}
	}

	// Proxy the request
	if p.proxy != nil {
		p.proxy.ServeHTTP(c, c.Request)
	} else {
		http.Error(c, "Proxy not initialized", http.StatusInternalServerError)
	}
}

// Stop stops the running process (useful for cleanup)
func (p *processSiteImpl) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.started {
		return nil
	}

	// Cancel the monitor goroutine, which will kill the process group
	if p.cancel != nil {
		p.cancel()
	}

	// Wait a bit for cleanup
	time.Sleep(100 * time.Millisecond)

	p.started = false
	return nil
}

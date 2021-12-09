package denorunner

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha1"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"text/template"
	"time"

	"github.com/pkg/errors"
)

type Config struct {
	WorkDir  string
	DenoPath string
}


// ======= Function ============
type DenoFunctionInstance struct {
	config      *Config
	cmd         *exec.Cmd
	lastInvoked time.Time
	runLock     sync.Mutex
	serverURL   string
	tempDir     string
	denoExited  chan error
}

func (inst *DenoFunctionInstance) LastInvoked() time.Time {
	return inst.lastInvoked
}

func (inst *DenoFunctionInstance) DidExit() chan error {
	return inst.denoExited
}

// All these files will be copied into a temporary function directory that deno will be invoked on

//go:embed deno-runtime-template/*.ts
var denoFiles embed.FS

func copyDenoFiles(destDir string) error {
	dirEntries, _ := denoFiles.ReadDir("deno-runtime-template")
	for _, file := range dirEntries {
		buf, err := denoFiles.ReadFile(fmt.Sprintf("deno-runtime-template/%s", file.Name()))
		if err != nil {
			return errors.Wrap(err, "read file")
		}
		if err := os.WriteFile(fmt.Sprintf("%s/%s", destDir, file.Name()), buf, 0600); err != nil {
			return errors.Wrap(err, "write file")
		}
	}
	return nil
}

//go:embed deno-runtime-template/template.js
var denoFunctionTemplate string

func wrapScript(initJSON string, code string) string {
	data := struct {
		Code     string
		InitData string
	}{
		Code:     code,
		InitData: initJSON,
	}
	tmpl, err := template.New("sourceTemplate").Parse(denoFunctionTemplate)
	if err != nil {
		log.Fatal("Could not render javascript:", err)
	}
	var out bytes.Buffer
	if err := tmpl.Execute(&out, data); err != nil {
		log.Fatal("Could not render javascript:", err)
	}
	return out.String()
}

type functionHash string

// Generates a content-based hash to be used as unique identifier for this function
func newFunctionHash(code string) functionHash {
	h := sha1.New()
	h.Write([]byte(code))
	bs := h.Sum(nil)
	return functionHash(fmt.Sprintf("%x", bs))
}

func NewDenoFunctionInstance(ctx context.Context, config *Config, logCallback func(message string), initData interface{}, code string) (*DenoFunctionInstance, error) {
	inst := &DenoFunctionInstance{
		config: config,
	}

	// Create deno project for function
	denoDir := fmt.Sprintf("%s/.deno/function-%s", config.WorkDir, newFunctionHash(code))
	if err := os.MkdirAll(denoDir, 0700); err != nil {
		return nil, errors.Wrap(err, "create deno dir")
	}
	inst.tempDir = denoDir

	if err := copyDenoFiles(denoDir); err != nil {
		return nil, errors.Wrap(err, "copy deno files")
	}

	if err := os.WriteFile(fmt.Sprintf("%s/function.js", denoDir), []byte(wrapScript(MustJsonString(initData), code)), 0600); err != nil {
		return nil, errors.Wrap(err, "write JS function file")
	}

	// Find an available TCP port to bind the function server to
	listenPort := FindFreePort(8000)

	// Run deno as child process with only network and environment variable access
	inst.cmd = exec.Command(config.DenoPath, "run", "--unstable", "--allow-net", "--allow-env", fmt.Sprintf("%s/function_server.ts", denoDir), fmt.Sprintf("%d", listenPort))

	// Don't propagate Ctrl-c to children
	inst.cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}
	inst.cmd.Env = append(inst.cmd.Env,
		"NO_COLOR=1",
		fmt.Sprintf("DENO_DIR=%s/.deno/cache", config.WorkDir))

	stdoutPipe, err := inst.cmd.StdoutPipe()
	if err != nil {
		return nil, errors.Wrap(err, "stdout pipe")
	}
	stderrPipe, err := inst.cmd.StderrPipe()
	if err != nil {
		return nil, errors.Wrap(err, "stderr pipe")
	}

	// Kick off the command in the background
	// Making it buffered to prevent go-routine leak (we don't care for the result after initial start-up)
	inst.denoExited = make(chan error, 1)
	if err := inst.cmd.Start(); err != nil {
		return nil, errors.Wrap(err, "deno run")
	}
	//log.Errorf("STARTING %s", name)

	// This is the point where we have a subprocess running which we may want to kill if we don't boot successfully
	// This will be set to true at the end, if it's not set, some error occured along the way
	everythingOk := false
	defer func() {
		if !everythingOk && inst.cmd.Process != nil {
			//log.Info("Hard killing deno process because of error")
			inst.Close()
		}
	}()

	go func() {
		inst.denoExited <- inst.cmd.Wait()
	}()

	// Listen to the stderr and log pipes and ship everything to logChannel
	bufferedStdout := bufio.NewReader(stdoutPipe)
	bufferedStderr := bufio.NewReader(stderrPipe)

	// Send stdout and stderr to the log channel
	go pipeLogStreamToCallback(bufferedStdout, logCallback)
	go pipeLogStreamToCallback(bufferedStderr, logCallback)

	inst.serverURL = fmt.Sprintf("http://localhost:%d", listenPort)

	// Wait for server to come up
waitLoop:
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-inst.denoExited:
			return nil, errors.New("deno exited on boot")
		default:
		}
		_, err := net.Dial("tcp", fmt.Sprintf("localhost:%d", listenPort))
		if err == nil {
			break waitLoop
		}
		time.Sleep(100 * time.Millisecond)
	}

	everythingOk = true

	return inst, nil
}

// Somewhat cleanly stop the deno process and clean up the temporary source files
func (inst *DenoFunctionInstance) Close() {
	if inst.cmd.Process != nil {
		inst.cmd.Process.Kill()
	}

	if err := os.RemoveAll(inst.tempDir); err != nil {
		fmt.Printf("Could not delete directory %s: %s\n", inst.tempDir, err)
	}
}


var ProcessExitedError = errors.New("process exited")

func (inst *DenoFunctionInstance) Invoke(ctx context.Context, event interface{}) (interface{}, error) {
	type jsError struct {
		Message string `json:"message"`
		Stack   string `json:"stack"`
	}

	// Instance can only be used sequentially for now
	inst.runLock.Lock()
	defer inst.runLock.Unlock()

	inst.lastInvoked = time.Now()

	if inst.cmd.ProcessState != nil && inst.cmd.ProcessState.Exited() {
		return nil, ProcessExitedError
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, inst.serverURL, strings.NewReader(MustJsonString(event)))
	if err != nil {
		return nil, errors.Wrap(err, "invoke call")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, errors.Wrap(err, "function http request")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("HTTP Error: %s", body)
	}

	var result interface{}
	jsonDecoder := json.NewDecoder(resp.Body)
	if err := jsonDecoder.Decode(&result); err != nil {
		return nil, errors.Wrap(err, "unmarshall response")
	}
	if errorMap, ok := result.(map[string]interface{}); ok {
		if errorObj, ok := errorMap["error"]; ok {
			var jsError jsError
			err = json.Unmarshal([]byte(MustJsonString(errorObj)), &jsError)
			if err != nil {
				return nil, fmt.Errorf("Runtime error: %s", MustJsonString(errorObj))
			}
			return nil, fmt.Errorf("Runtime error: %s\n%s", jsError.Message, jsError.Stack)

		}
	}

	return result, nil
}

func pipeLogStreamToCallback(bufferedReader *bufio.Reader, callback func(message string)) {
readLoop:
	for {
		line, err := bufferedReader.ReadString('\n')
		if err != nil {
			break readLoop
		}
		callback(line)
	}
}

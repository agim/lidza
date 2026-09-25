// Package engine runs pack WASM modules in wazero: a module compiled once,
// a bounded pool of pre-warmed instances, and calls with a deadline that
// pass JSON through linear memory (the ABI in core/src/abi.rs).
package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

// Options bounds a module.
type Options struct {
	// MemoryMB caps each instance's linear memory; default 64.
	MemoryMB int
	// Uninterruptible drops the deadline checks the compiler otherwise
	// inserts at every loop back-edge. Loops run several times faster; a
	// call that overruns the pool deadline then returns an error to its
	// caller but keeps running inside its instance until it finishes, and
	// that instance is discarded afterwards. For capabilities whose loops
	// are bounded by their input.
	Uninterruptible bool
}

// Module is a compiled WASM module.
type Module struct {
	rt       wazero.Runtime
	compiled wazero.CompiledModule
	exports  []string
	hasInit  bool
}

// Compile compiles wasm with WASI available to it and no filesystem, clock
// or network beyond what WASI preview 1 exposes.
func Compile(ctx context.Context, wasm []byte, opt Options) (*Module, error) {
	if opt.MemoryMB <= 0 {
		opt.MemoryMB = 64
	}
	cfg := wazero.NewRuntimeConfig().
		WithCloseOnContextDone(!opt.Uninterruptible).
		WithMemoryLimitPages(uint32(opt.MemoryMB * 16)) // 64 KiB pages
	rt := wazero.NewRuntimeWithConfig(ctx, cfg)
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, rt); err != nil {
		rt.Close(ctx)
		return nil, err
	}
	compiled, err := rt.CompileModule(ctx, wasm)
	if err != nil {
		rt.Close(ctx)
		return nil, fmt.Errorf("compile wasm: %w", err)
	}
	m := &Module{rt: rt, compiled: compiled}
	for name := range compiled.ExportedFunctions() {
		m.exports = append(m.exports, name)
		if name == "_initialize" {
			m.hasInit = true
		}
	}
	return m, nil
}

// Exports lists the exported function names.
func (m *Module) Exports() []string { return append([]string(nil), m.exports...) }

// Close releases the runtime and every instance made from the module.
func (m *Module) Close(ctx context.Context) error { return m.rt.Close(ctx) }

// instance is one instantiation with its own memory. Its exported
// functions are looked up once: wazero builds a wrapper per lookup, which
// costs more than the call itself.
type instance struct {
	mod   api.Module
	alloc api.Function
	free  api.Function
	fns   map[string]api.Function
}

func (m *Module) instantiate(ctx context.Context) (*instance, error) {
	cfg := wazero.NewModuleConfig().WithName("")
	if m.hasInit {
		cfg = cfg.WithStartFunctions("_initialize")
	} else {
		cfg = cfg.WithStartFunctions()
	}
	mod, err := m.rt.InstantiateModule(ctx, m.compiled, cfg)
	if err != nil {
		return nil, fmt.Errorf("instantiate: %w", err)
	}
	in := &instance{mod: mod, alloc: mod.ExportedFunction("lidza_alloc"), free: mod.ExportedFunction("lidza_free"), fns: map[string]api.Function{}}
	if in.alloc == nil || in.free == nil {
		mod.Close(ctx)
		return nil, errors.New("module does not export lidza_alloc and lidza_free (missing abi.rs?)")
	}
	for _, name := range m.exports {
		if f := mod.ExportedFunction(name); f != nil {
			in.fns[name] = f
		}
	}
	return in, nil
}

// call runs fn with input and returns the output JSON.
func (in *instance) call(ctx context.Context, fn string, input []byte) ([]byte, error) {
	f := in.fns[fn]
	if f == nil {
		return nil, fmt.Errorf("capability %q is not exported by the module", fn)
	}
	mem := in.mod.Memory()
	res, err := in.alloc.Call(ctx, uint64(len(input)))
	if err != nil {
		return nil, err
	}
	inPtr := uint32(res[0])
	if len(input) > 0 && !mem.Write(inPtr, input) {
		return nil, errors.New("input does not fit in module memory")
	}
	res, err = f.Call(ctx, uint64(inPtr), uint64(len(input)))
	if err != nil {
		return nil, err
	}
	_, _ = in.free.Call(ctx, uint64(inPtr), uint64(len(input)))
	outPtr, outLen := uint32(res[0]>>32), uint32(res[0])
	data, ok := mem.Read(outPtr, outLen)
	if !ok {
		return nil, errors.New("output pointer is out of module memory")
	}
	out := append([]byte(nil), data...)
	_, _ = in.free.Call(ctx, uint64(outPtr), uint64(outLen))
	var probe struct {
		Err *string `json:"$error"`
	}
	if json.Unmarshal(out, &probe) == nil && probe.Err != nil {
		return nil, &CallError{Capability: fn, Message: *probe.Err}
	}
	return out, nil
}

// CallError is an error the capability itself returned.
type CallError struct {
	Capability string
	Message    string
}

func (e *CallError) Error() string { return e.Capability + ": " + e.Message }

// ErrPoolBusy is returned when no instance frees up before the deadline.
var ErrPoolBusy = errors.New("engine: every instance is busy")

// Pool is a bounded set of pre-warmed instances. A call takes one, runs
// with the pool's deadline, and returns it; an instance whose call was
// cut off by the deadline is discarded and replaced.
type Pool struct {
	m       *Module
	idle    chan *instance
	timeout time.Duration

	calls, errors, timeouts, busy atomic.Int64
}

// Stats reports counters and the pool state for /metrics.
func (p *Pool) Stats() map[string]float64 {
	return map[string]float64{
		"pool_size":      float64(cap(p.idle)),
		"pool_idle":      float64(len(p.idle)),
		"calls_total":    float64(p.calls.Load()),
		"errors_total":   float64(p.errors.Load()),
		"timeouts_total": float64(p.timeouts.Load()),
		"busy_total":     float64(p.busy.Load()),
	}
}

// NewPool instantiates size instances up front. timeout bounds every call.
func NewPool(ctx context.Context, m *Module, size int, timeout time.Duration) (*Pool, error) {
	if size <= 0 {
		size = 1
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	p := &Pool{m: m, idle: make(chan *instance, size), timeout: timeout}
	for i := 0; i < size; i++ {
		in, err := m.instantiate(ctx)
		if err != nil {
			p.Close(ctx)
			return nil, err
		}
		p.idle <- in
	}
	return p, nil
}

// Call runs the capability fn with a JSON input and returns its JSON
// output. It waits for an instance no longer than the pool timeout.
func (p *Pool) Call(ctx context.Context, fn string, input []byte) ([]byte, error) {
	p.calls.Add(1)
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	var in *instance
	select {
	case in = <-p.idle:
	case <-ctx.Done():
		p.busy.Add(1)
		return nil, fmt.Errorf("%w (%v)", ErrPoolBusy, ctx.Err())
	}
	out, err := in.call(ctx, fn, input)
	if err != nil {
		p.errors.Add(1)
	}
	if err != nil && ctx.Err() != nil {
		p.timeouts.Add(1)
		// The deadline closed the instance mid-call; make a fresh one.
		in.mod.Close(context.Background())
		fresh, ierr := p.m.instantiate(context.Background())
		if ierr != nil {
			// Leave the slot empty rather than block forever; the next
			// caller sees ErrPoolBusy and the log has the cause.
			return nil, fmt.Errorf("call cut off by deadline and instance could not be replaced: %v (%w)", ierr, err)
		}
		p.idle <- fresh
		if errors.Is(ctx.Err(), context.Canceled) {
			return nil, fmt.Errorf("%s: call cancelled", fn)
		}
		return nil, fmt.Errorf("%s: deadline exceeded after %s", fn, p.timeout)
	}
	p.idle <- in
	return out, err
}

// CallJSON encodes in, calls fn and decodes into out.
func CallJSON(ctx context.Context, p *Pool, fn string, in, out any) error {
	data, err := json.Marshal(in)
	if err != nil {
		return err
	}
	res, err := p.Call(ctx, fn, data)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(res, out)
}

// Close closes every idle instance; in-flight calls finish first up to
// the timeout.
func (p *Pool) Close(ctx context.Context) error {
	deadline := time.After(p.timeout)
	for i := 0; i < cap(p.idle); i++ {
		select {
		case in := <-p.idle:
			in.mod.Close(ctx)
		case <-deadline:
			return errors.New("engine: instances still busy at close")
		}
	}
	return nil
}

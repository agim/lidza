package pack

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/agim/lidza/pkg/engine"
	"github.com/agim/lidza/pkg/schema"
)

// TestOfficialMedia builds the media pack as an app gets it (lidza pack
// add media, then lidza gen) and runs its capabilities: fit contain and
// cover, and the unsupported_format code for HEIC, AVIF and TIFF.
func TestOfficialMedia(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a Rust crate")
	}
	if _, err := exec.LookPath("cargo"); err != nil {
		t.Skip("cargo not installed")
	}
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, schema.FileName), []byte("type Greeting {\n  name string\n}\n"), 0o644)
	if _, _, err := Add(root, "media"); err != nil {
		t.Fatal(err)
	}
	s, err := schema.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Generate(root, "app", "", []string{"media"}, s); err != nil {
		t.Fatal(err)
	}
	m, err := Load(root, "media")
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(os.TempDir(), "lidza-media-test-target")
	cmd := exec.Command("cargo", "build", "--release", "--target", "wasm32-wasip1", "--quiet")
	cmd.Dir = filepath.Join(root, m.CrateDir())
	cmd.Env = append(os.Environ(), "CARGO_TARGET_DIR="+target)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("cargo build: %v\n%s", err, out)
	}
	wasm, err := os.ReadFile(filepath.Join(target, "wasm32-wasip1", "release", "media.wasm"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	mod, err := engine.Compile(ctx, wasm, engine.Options{MemoryMB: 256, Uninterruptible: true})
	if err != nil {
		t.Fatal(err)
	}
	defer mod.Close(ctx)
	pool, err := engine.NewPool(ctx, mod, 1, 20*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close(ctx)

	// 40x20: red, then green across the middle half, then blue.
	src := image.NewRGBA(image.Rect(0, 0, 40, 20))
	for x := 0; x < 40; x++ {
		c := color.RGBA{0, 200, 0, 255}
		if x < 10 {
			c = color.RGBA{200, 0, 0, 255}
		} else if x >= 30 {
			c = color.RGBA{0, 0, 200, 255}
		}
		for y := 0; y < 20; y++ {
			src.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	png.Encode(&buf, src)

	type result struct {
		Data   []byte `json:"data"`
		Width  int    `json:"width"`
		Height int    `json:"height"`
		Format string `json:"format"`
	}
	resize := func(in map[string]any) (result, error) {
		var out result
		err := engine.CallJSON(ctx, pool, "image_resize", in, &out)
		return out, err
	}

	// Contain: inside 10x10, aspect kept: 10x5 with the red edge.
	out, err := resize(map[string]any{"data": buf.Bytes(), "width": 10, "height": 10, "format": "png"})
	if err != nil || out.Width != 10 || out.Height != 5 {
		t.Fatalf("contain: %+v %v", out, err)
	}
	// Cover: exactly 10x10, the centre kept: green, no red or blue left.
	out, err = resize(map[string]any{"data": buf.Bytes(), "width": 10, "height": 10, "fit": "cover", "format": "png"})
	if err != nil || out.Width != 10 || out.Height != 10 || out.Format != "png" {
		t.Fatalf("cover: %+v %v", out, err)
	}
	img, err := png.Decode(bytes.NewReader(out.Data))
	if err != nil || img.Bounds().Dx() != 10 || img.Bounds().Dy() != 10 {
		t.Fatalf("cover output: %v %v", img.Bounds(), err)
	}
	for _, x := range []int{1, 5, 8} {
		r, g, b, _ := img.At(x, 5).RGBA()
		if g>>8 < 150 || r>>8 > 60 || b>>8 > 60 {
			t.Errorf("cover pixel %d: %d %d %d, want the green centre", x, r>>8, g>>8, b>>8)
		}
	}
	// Cover enlarges a smaller image to the size asked for.
	if out, err := resize(map[string]any{"data": buf.Bytes(), "width": 64, "height": 64, "fit": "cover"}); err != nil || out.Width != 64 || out.Height != 64 {
		t.Fatalf("cover up: %+v %v", out, err)
	}
	if _, err := resize(map[string]any{"data": buf.Bytes(), "width": 10, "fit": "cover"}); err == nil || engine.ErrorCode(err) != "" {
		t.Fatalf("cover without height: %v", err)
	}

	// Formats the pack cannot decode carry the code.
	heic := append([]byte{0, 0, 0, 24}, []byte("ftypheic\x00\x00\x00\x00mif1heic")...)
	avif := append([]byte{0, 0, 0, 24}, []byte("ftypavif\x00\x00\x00\x00mif1avif")...)
	tiff := []byte("II*\x00\x08\x00\x00\x00\x00\x00\x00\x00")
	for name, data := range map[string][]byte{"heic": heic, "avif": avif, "tiff": tiff, "garbage": []byte("not an image at all")} {
		_, err := resize(map[string]any{"data": data, "width": 10})
		if engine.ErrorCode(err) != "unsupported_format" {
			t.Errorf("%s resize: %v (code %q)", name, err, engine.ErrorCode(err))
		}
		var info map[string]any
		err = engine.CallJSON(ctx, pool, "image_info", map[string]any{"data": data}, &info)
		if engine.ErrorCode(err) != "unsupported_format" {
			t.Errorf("%s info: %v (code %q)", name, err, engine.ErrorCode(err))
		}
	}
	// A damaged file of a known format is not "unsupported".
	if _, err := resize(map[string]any{"data": buf.Bytes()[:60], "width": 10}); err == nil || engine.ErrorCode(err) == "unsupported_format" {
		t.Errorf("truncated png: %v (code %q)", err, engine.ErrorCode(err))
	}
}

# Laya in Go: design for a standalone port

This is a design for running [Laya](https://huggingface.co/convaiinnovations/laya) inference in Go,
as a separate Go module that System 1 Arcade and other programs can import. It would replace the
built-in agent's Python server (`agents/laya_server.py`), and with it the Python 3.10 + PyTorch
requirement.

**Status:** proposal. Nothing here is built. The module path `github.com/brennanMKE/laya-go` is a
placeholder name. Facts about Laya come from the installed package (`laya` 0.3.6 in `.venv`) and
the cached checkpoint (revision `1c5edc17a7acd8701df6fc341c0d179f1c62c982`). Measurements were
taken on this Mac (Apple M4 Pro, 8 performance + 4 efficiency cores, 64 GB, Go 1.27.1) on
2026-09-22. Anything marked **Unverified** was not checked, and anything marked **Estimate** was
not measured.

## Summary

- **Build our own Go forward pass rather than wrapping a runtime.** The model is small and fixed:
  a 28-layer ModernBERT-large encoder, a 2-layer transformer head, and two small MLPs. Tokenizing,
  building prompts, calibration and the HTTP contract are all plain Go. Only the matrix multiplies
  need a fast native kernel.
- **Put the forward pass behind a pluggable `Engine` interface.** The default engine runs on a small
  `Kernels` interface. Its kernels are:
  - **Apple Accelerate on macOS**, loaded through [purego](https://github.com/ebitengine/purego)
    with `CGO_ENABLED=0`. Measured here at 1.4–2.5 TFLOP/s for Laya's matrix shapes.
  - **Portable Go kernels** everywhere else. These are the reference implementation. They are too
    slow for real-time play until they get SIMD.

  Two optional engines come later:
  - **ONNX Runtime** for fast Windows and Linux.
  - **Metal** for GPU speed on Macs.
- **Expect CPU parity with PyTorch, not GPU parity, at first.** The measured matrix-multiply time
  for a full Laya pass through Accelerate is close to PyTorch's own CPU time: 71 ms against 81 ms
  for one state, and 395 ms (14 states) against 431 ms (16 states) for a Tetris-sized batch. That
  is 2–4 times slower than today's MPS GPU path for multi-state decisions. Matching MPS needs the
  Metal engine (phase 5).
- **Nothing native to ship on macOS, and Windows cross-compilation keeps working.** Accelerate is
  part of macOS, purego needs no C toolchain, and the Windows build doesn't change.
- **Prove parity with golden fixtures from the Python reference.** Token ids must match exactly.
  Logits must match within 1e-3. The 4-decimal answers the app sees must match. PyTorch's CPU and
  MPS runs already agree to 4 decimals on the game prompts (see [Parity testing](#parity-testing)).

## Why

The built-in agent today works like this (`agent.go`):

1. The app finds Python 3.10 or newer, creates a venv in `~/Library/Application Support/System 1
   Arcade/agent`, and runs `pip install laya`. That downloads PyTorch and friends, about 1 GB, and
   takes a few minutes.
2. It writes the embedded `laya_server.py` and `laya_batch.py`, starts them as a child process, and
   waits for the line `agent ready http://…/predict`.
3. Each decision goes to that server as JSON over HTTP (`internal/agent/client.go`).

This costs a Python requirement that users must satisfy themselves, a slow first start, about
20 seconds from app start to a loaded model, a second process to supervise, and a Windows path that
hasn't been run. A Go library loads the model in the app's own process. The only first-run cost is
the 804 MB weights download that happens today anyway.

## Goals and non-goals

Goals:

- Answer Laya's typed questions (`choice`, `score`, `noul`) with the same answers as the Python
  package, within the tolerances below, for the English checkpoint `convaiinnovations/laya`.
- A standalone Go module with a small public API (`Load`, `Predict`, `PredictMany`), usable by any
  Go program, and a server command that is a drop-in replacement for `agents/laya_server.py`.
- Builds with `CGO_ENABLED=0` on macOS, Windows and Linux. Native acceleration comes from system
  libraries or optional engines, never from a required C toolchain.
- Download weights from the Hugging Face Hub at a pinned revision, sharing the Python cache layout
  so a model already downloaded by Python is reused.
- Speed on Apple Silicon at least equal to PyTorch on the CPU in phase 2, and to PyTorch on MPS
  once the Metal engine lands.

Non-goals:

- Training, fine-tuning, or fitting temperatures. The TD(λ) targets, proper-scoring reward and ECE
  helpers in `common.py` stay in Python.
- Running arbitrary Hugging Face models. Only the Laya architecture is supported.
- The embedding shortlist (`shortlist.py`), email helpers (`email.py`) and question presets
  (`presets.py`) in the first release. They are small and can follow, but System 1 Arcade doesn't
  use them.
- The multilingual checkpoint and the `Router` in the first release (see phase 6).
- Bit-exact equality with PyTorch. Float summation order differs; the tolerances below define
  "the same".

## What Laya computes

This section restates the Python reference precisely enough to port. File references are to
`.venv/lib/python3.14/site-packages/laya/`.

### Checkpoint files

`Agent.__init__` downloads only these files (`snapshot_download` with `allow_patterns`):

| File | Size | Contents |
|---|---|---|
| `model.safetensors` | 842,609,210 bytes (804 MB) | 206 tensors: 205 F16 and one F32 (`temperature`, unused at inference). 421,293,830 parameters |
| `rl_agent_config.json` | 745 bytes | `max_len` 512, `head_max_len` 192, `head_layers` 2, `act_costs` `{"escalate": 0.5}`, the fitted temperatures |
| `encoder/config.json` | 2 KB | ModernBERT config: hidden 1024, 28 layers, 16 heads, intermediate 2624, vocab 50368, `local_attention` 128, `global_attn_every_n_layers` 3, RoPE θ 160000 (global) and 10000 (sliding), `norm_bias` false, `attention_bias` false, `mlp_bias` false, `norm_eps` 1e-5, `hidden_activation` "gelu" |
| `tokenizer/tokenizer.json` | 3.6 MB | byte-level BPE: NFC normalizer, 50,280-entry vocab, 50,009 merges, 116 added tokens |
| `tokenizer/tokenizer_config.json` | 308 bytes | special tokens `[CLS]` 50281, `[SEP]` 50282, `[PAD]` 50283, `[MASK]` 50284 |

`max_prefixes` (6) and `cost_wrong_act` in the config are not read by the inference code. The
config says `"dtype": "float32"`, but the weights are stored as fp16.

Where the parameters are:

| Part | Tensors | Parameters |
|---|---|---|
| Token embeddings | `encoder.embeddings.tok_embeddings.weight` [50368, 1024] | 51.6M |
| 28 encoder layers | `attn.Wqkv` [3072, 1024], `attn.Wo` [1024, 1024], `mlp.Wi` [5248, 1024], `mlp.Wo` [1024, 2624], `attn_norm` (not in layer 0), `mlp_norm` | 343.2M |
| 2 head layers (`nn.TransformerEncoderLayer`) | `self_attn.in_proj_weight` [3072, 1024] + bias, `out_proj` [1024, 1024] + bias, `linear1` [4096, 1024] + bias, `linear2` [1024, 4096] + bias, `norm1`, `norm2` (with bias) | 25.2M |
| Type embeddings, scorer, act head | `type_emb` [3, 1024]; `scorer.0` LayerNorm, `scorer.1` [1024, 1024], `scorer.3` [1, 1024]; `act_head.0` [256, 1028], `act_head.2` [2, 256] | 1.3M |

### Building one sequence per question

`common.build_sequence` makes one token sequence per (state, question) pair. It has this shape:

```
[CLS] "<type> question: <instructions>" [SEP] [MASK] " opt0" [MASK] " opt1" … [SEP] <state> [SEP]
```

The rules, in order:

1. **Options are rendered as text** (`render_options`):
   - A choice option is `"name"`, or `"name: description"` when the description isn't `None` or
     `""`.
   - A score level is `"level i: text"`.
   - A noul question always has two options:
     - `"false: …"`, defaulting to `"no, the statement does not hold"`
     - `"true: …"`, defaulting to `"yes, the statement holds"`

   A non-string criterion value is compact JSON with `", "` and `": "` separators.
2. **Instructions** that aren't strings become `json.dumps(…)`. Any `[MASK]` text in instructions,
   options or state is replaced by a space.
3. **Each option** is `[MASK]` plus the tokens of `" " + option`, cut to 48 tokens.
4. **Budget:** `opt_budget = head_max_len − total option tokens`. If that is under 16, each option
   is cut to `max(4, (head_max_len − 16) // n_options)` tokens. The instruction tokens are then cut
   to `max(8, opt_budget)`.
5. **Markers:** the position of each option's `[MASK]` is its marker.
6. **State:** the state (a string, or `json.dumps(state, ensure_ascii=False)` for a dict or list) is
   tokenized and cut from the right to fit `max_len − len(prefix) − 1`. Then the final `[SEP]` is
   added.
7. **Too many options:** if a marker falls past `max_len`, the question raises "options exceed
   head_max_len".

Every tokenizer call uses `add_special_tokens=False`, so the tokenizer's post-processor is never
used. Game prompts are short. On this Mac, a Tetris spot is 56 tokens, a Frogger hop 55, and a
Space Invaders bomb question 43.

### The network

`DecisionModel.forward`, with shapes for n sequences of up to L tokens (hidden d = 1024). The
encoder is ModernBERT, as implemented in transformers 5.17.0 `modeling_modernbert.py`:

1. **Embeddings:** `LayerNorm(tok_embeddings[ids])`. There are no position embeddings, and the
   LayerNorm has no bias.
2. **28 pre-norm layers.** For layer i:

   ```
   x = x + Wo · Attn(RoPE(q, k), v)   with q, k, v from Wqkv · attn_norm(x)
   x = x + Wo · (GELU(a) ⊙ g)         with a, g = split(Wi · mlp_norm(x))
   ```

   Details that matter for parity:
   - Layer 0's `attn_norm` is the identity (the checkpoint has no tensor for it).
   - `Wqkv` output is laid out as [3, heads, 64]: q, then k, then v.
   - RoPE is the non-interleaved "rotate half" form, computed in fp32:
     - inverse frequency `θ^(−2j/64)`
     - `cos`/`sin` over `cat(freqs, freqs)`
     - positions `0…L−1`
   - Layers 0, 3, 6, … 27 are global attention with θ 160000. The rest are sliding-window
     attention with θ 10000.
   - The sliding window lets token i attend to token j only when |i − j| ≤ 64. A token sees at
     most 129 others.
   - Attention scale is 1/√64. Attention is bidirectional and ignores padding keys.
   - The MLP is GeGLU. The **first** half of `Wi`'s 5248 outputs goes through exact (erf) GELU; the
     second half is the gate.
   - All LayerNorms have no bias and use eps 1e-5.
3. **Final norm:** one more LayerNorm on the encoder output.
4. **Question type:** add `type_emb[qtype]`, where choice = 0, score = 1 and noul = 2, to every
   token.
5. **Head: two `nn.TransformerEncoderLayer` layers** (16 heads, feed-forward 4096). They are
   `norm_first=True` and use PyTorch's default **ReLU** activation:

   ```
   x = x + out_proj · MHA(in_proj · norm1(x))    (padding keys masked)
   x = x + linear2 · ReLU(linear1 · norm2(x))
   ```

   These LayerNorms and linears have biases. There is no RoPE in the head.
6. **Scorer:** gather the hidden state at each marker and apply
   `Linear(1) · GELU · Linear(1024) · LayerNorm`. That gives one logit per option. Empty option
   slots are set to −1e4.
7. **Act head:** `Linear(2) · GELU · Linear(256)` over `[h[CLS], top1, top1 − top2, entropy / log k,
   k / 255]`, where the probabilities come from an untempered softmax of the logits. Its softmax is
   `action.act_probability` in `Agent.predict` output. The app doesn't read it.

**Unpadding.** The Python code pads a batch to the longest sequence and masks. Right padding
doesn't move positions, and masked keys don't touch real tokens. So a port can pack all sequences
back to back and run attention per sequence. That is how the Go engine does it, and no compute is
spent on padding. On this Mac, PyTorch gave identical 4-decimal answers for 21 game prompts asked
one at a time and batched together. For states of 65 tokens or fewer, which covers every current
game prompt, the sliding window covers the whole sequence and sliding layers behave like global
ones except for the RoPE θ.

### Calibration and answers

`Agent.system_one` turns logits into answers:

1. **Temperature.** Look up the bucket `"<type>:<size>"`, where size is `2`, `3-5`, `6-10` or
   `11+` options. If the bucket is missing, use the per-type temperature. Clamp to [0.5, 5.0]; the
   checkpoint's `choice:11+` value of 0.1006 is clamped to 0.5, with a warning. Divide the logits
   by the temperature and take the softmax over the k real options.
2. **Answer, by type:**
   - **choice:** `choice` is the argmax label. `probabilities` holds every label, in criteria
     order. `confidence = 1 − H(p) / log k`.
   - **score:** `score = Σ i·pᵢ`, plus `legend`, `probabilities` keyed `"0"…`, and `confidence`.
   - **noul:** `noul = p[1]` and `confidence = max(p1, 1 − p1)`.
3. **Rounding.** Every number is rounded to 4 decimals. `usage.input_tokens` counts the real
   (unpadded) tokens.

`agents/laya_batch.py`'s `predict_many` runs the same math over many (state, questions) prompts in
one pass. Its output is slightly smaller: it has no `action`, and `score` answers carry no
`confidence` or `legend`.

## Public Go API (sketch)

One package, `laya`, plus subpackages for pieces other programs may want on their own.

```go
package laya // github.com/brennanMKE/laya-go (proposed name)

// Loading -----------------------------------------------------------------

type Options struct {
	Repo      string // default "convaiinnovations/laya"
	Revision  string // default: the commit this release was tested against
	Subfolder string // "" (English) or "typed-decisions"; "multilingual" later
	Dir       string // load a local checkpoint directory instead of the Hub
	CacheDir  string // default: $HF_HUB_CACHE, $HF_HOME/hub, ~/.cache/huggingface/hub
	Token     string // default: $HF_TOKEN
	Engine    string // "auto" (default), "native", "ort", "metal"
	Threads   int    // 0 = runtime.NumCPU()
	Progress  func(Progress) // download and load progress, for a UI
}

func Load(ctx context.Context, opts Options) (*Model, error)
func (m *Model) Close() error
func (m *Model) Info() Info // engine, kernels, revision, max_len, head_max_len, temperatures

// Questions ---------------------------------------------------------------

type Type uint8 // Choice, Score, Noul (the qtype ids 0, 1, 2)

type Question struct {
	Type         Type
	Instructions string
	Options      []Option // choice: in order; the order is part of the prompt
	Levels       []string // score: lowest first
	True, False  string   // noul: optional descriptions
}

type Option struct{ Name, Description string }

func Choice(instructions string, opts ...Option) Question
func ChoiceMap(instructions string, m map[string]string) Question // options sorted by name
func Noul(instructions string) Question
func Score(instructions string, levels ...string) Question

// State is a string, or JSON serialized the way Python's json.dumps does.
type State struct{ /* text */ }

func Text(s string) State
func JSON(raw json.RawMessage) State // keeps key order; Python spacing and escaping

// Asking ------------------------------------------------------------------

type Prompt struct {
	Key       string // batch key, e.g. "up" or "p12s3"
	State     State
	Questions []Named // in order
}

type Named struct {
	Name string
	Question
}

// Predict answers every question about one state in one forward pass.
func (m *Model) Predict(ctx context.Context, state State, qs ...Named) (*Result, error)

// PredictMany answers many prompts in one forward pass (split into chunks
// of at most MaxBatchTokens), like agents/laya_batch.py's predict_many.
func (m *Model) PredictMany(ctx context.Context, prompts []Prompt) ([]*Result, error)

type Result struct {
	Answers map[string]Answer
	Usage   Usage // InputTokens
}

type Answer struct {
	Type           Type
	Choice         string  // choice
	Score          float64 // score: expected level
	Noul           float64 // noul: P(true)
	Probabilities  []Prob  // option order; marshals to a JSON object in that order
	Legend         []string
	Confidence     float64
	ActProbability float64
}

type Prob struct {
	Label string
	P     float64
}
```

Notes on the API:

- **Criteria order is part of the prompt.** Go maps have no order, so `Question` takes an ordered
  slice. `ChoiceMap` sorts by name. That matches what System 1 Arcade sends today:
  `game.Choice(…, map[string]string{…})` is encoded by `encoding/json`, which sorts map keys, so
  Frogger's options reach Laya as `deadly`, `safe`. Keeping that order keeps today's answers.
- **Python-compatible JSON for structured states.** `json.dumps` uses `", "` and `": "`, keeps
  non-ASCII characters, and keeps key order. Go's `encoding/json` sorts map keys, uses no spaces and
  escapes `<`, `>` and `&`. `State` and the criteria renderer therefore need a small
  Python-compatible encoder. receptron's Node port found the same problem and ported `json.dumps`
  by hand.
- **Unrounded numbers in Go, rounded numbers on the wire.** The Go API returns full-precision
  floats. The JSON encoder rounds to 4 decimals with `strconv.FormatFloat(x, 'f', 4, 64)`, which
  rounds correctly the way Python's `round(x, 4)` does. `math.Round(x*1e4)/1e4` does not.
- **Errors are typed.** `ErrOptionsTooLong` means the options exceed `head_max_len`. Other errors
  cover a choice with no criteria, a duplicate option name, and an unknown type.
- **Concurrency.** A `Model` is safe for concurrent use. Calls are queued, and each forward pass
  uses every core, so running two at once gains nothing on a CPU engine.

### Subpackages

| Package | Contents |
|---|---|
| `laya/tokenizer` | byte-level BPE that reads `tokenizer.json` (see [Tokenizer](#tokenizer)) |
| `laya/prompt` | `build_sequence` and `render_options`, exported for tools and tests |
| `laya/engine` | the `Engine` and `Kernels` interfaces, the native engine, and the kernel sets |
| `laya/hub` | download and cache compatible with the Hugging Face cache layout |
| `laya/wire` | the JSON contract: decodes `{state, questions}` and `{batch}` keeping key order; encodes answers the way the Python server does |
| `laya/answercache` | port of `AnswerCache` / `answer_cached`, with the same key format and stats |
| `cmd/laya-server` | HTTP server compatible with `agents/laya_server.py` |
| `cmd/laya` | CLI: `laya predict < request.json`, `laya bench`, `laya golden verify` |

### `cmd/laya-server`

This has the same flags, endpoints and startup line as `agents/laya_server.py`, so System 1 Arcade's
current launcher can run it as a drop-in replacement:

```
laya-server [--host 127.0.0.1] [--port 0] [--model convaiinnovations/laya] [--revision …]
            [--engine auto] [--cache-size N] [--stats-every S]

POST /predict  {"state": …, "questions": {…}}              -> {"answers": {name: answer}}
POST /predict  {"batch": {key: {"state", "questions"}}}    -> {"answers": {"key.name": answer}}
GET  /health   -> {"ok": true}
GET  /stats    -> answer cache stats (same fields as AnswerCache.stats())
stdout: "agent ready http://127.0.0.1:<port>/predict"
```

It honors `SYSTEM1_LAYA_CACHE` like the Python server. One small difference: it returns the full
answer, `Agent.predict`'s shape, for batches too. The extra fields (`confidence` and `legend` on
score answers, and `action`) are ignored by the app and by any client that reads the documented
fields in [custom-agents.md](custom-agents.md).

## Forward pass in Go

### Engines and kernels

```go
package engine

// Batch is every sequence of a call, packed back to back with no padding.
type Batch struct {
	Tokens   []int32   // all sequences, concatenated
	Starts   []int32   // len n+1; sequence i is Tokens[Starts[i]:Starts[i+1]]
	Markers  [][]int32 // option marker positions within each sequence
	QTypes   []uint8
	WantAct  bool
}

type Output struct {
	Logits [][]float32  // per sequence, one uncalibrated logit per option
	Act    [][2]float32 // act head softmax, when WantAct
}

// Engine runs the whole network. ONNX Runtime and Metal engines implement
// this directly; the native engine implements it on top of Kernels.
type Engine interface {
	Forward(ctx context.Context, b *Batch) (*Output, error)
	Close() error
}

// Kernels are the hot operations the native engine delegates.
type Kernels interface {
	Name() string
	// C[m,n] = A[m,k] · W[n,k]ᵀ + bias, with W in PyTorch Linear layout.
	MatMulT(c, a []float32, w *Weight, m int, bias []float32)
}

// Weight holds one Linear weight in whatever form the kernels want:
// fp32 for Accelerate, fp16 or int8 with scales for others.
type Weight struct { /* rows, cols, format, data */ }
```

The native engine owns everything that isn't a matrix multiply:

- embedding gather and fp16→fp32 conversion
- LayerNorm, with and without bias
- RoPE, with a cos/sin table per θ computed once for 512 positions
- attention per sequence and head
- GELU, GeGLU and ReLU
- the marker gather, scorer and act head

At game sizes, attention is small next to the linears. For 56 tokens it is about 0.2 GFLOP of the
41 GFLOP pass. Attention is a loop per (sequence, head): scores Q·Kᵀ over the keys the window
allows, a softmax in fp32, then a weighted sum of V. For long states it can call `MatMulT` on the
blocks.

Things that are easy to get wrong:

- **GELU is exact erf GELU.** Go's `math.Erf` is float64 and costs about 10 ns a call (**Estimate**).
  A 780-token Tetris batch has 57M GELU inputs in the encoder, which is 0.5 s on one core. Use a
  float32 erf approximation with max error under 1e-7, and run it across goroutines.
- **The head's activation is ReLU.** `nn.TransformerEncoderLayer` defaults to ReLU even though every
  other activation in Laya is GELU.
- **Layer 0 has no `attn_norm`.** The loader must accept that one tensor as missing. Every other
  tensor must be present with the expected shape, the same check `_verify_compatibility` makes.
- **The last head layer only needs some rows.** It only has to produce the marker rows and row 0
  (for the act head). Keys and values still need every row, but the query, output projection and
  feed-forward can skip the rest. This saves about 2% of the pass and is optional.
- **A debug hook for bisecting mismatches.** `Trace func(name string, rows [][]float32)` receives
  named intermediates (`emb`, `layer.0` … `layer.27`, `final_norm`, `head.0`, `head.1`,
  `logits`), so a parity failure can be traced to the first layer that disagrees.

### Kernel sets

| Kernels | Where | How | Status |
|---|---|---|---|
| `accelerate` | darwin (arm64, amd64) | `cblas_sgemm` from `/System/Library/Frameworks/Accelerate.framework`, loaded with `purego.Dlopen`. No cgo; nothing to ship | Measured here with `CGO_ENABLED=0` (below) |
| `portable` | everywhere | Go, blocked and parallel across goroutines | Reference implementation; slow |
| `simd` | amd64, arm64 | Go assembly GEMM microkernels. Options: write our own; use [go-highway](https://github.com/ajroetker/go-highway) (NEON/SME and AVX2/AVX-512, no GOEXPERIMENT); or Go 1.27's experimental `simd/archsimd` (amd64, and NEON new in 1.27, behind `GOEXPERIMENT=simd`) | Phase 5. go-highway's claims are **Unverified** |

## Runtime options compared

| Option | cgo | macOS arm64 | Windows amd64 | Linux | Mac GPU | Ships native code | Notes |
|---|---|---|---|---|---|---|---|
| **Native Go + Accelerate (recommended on macOS)** | no | fast (≈PyTorch CPU) | n/a | n/a | no | no | Accelerate is part of macOS |
| **Native Go + portable kernels** | no | slow | slow | slow | no | no | Reference and fallback; see the estimate below |
| Native Go + SIMD kernels | no | est. 0.5–1× Accelerate | est. usable | est. usable | no | no | Assembly to write and maintain; [rembed](https://github.com/rostamlabs/rembed) (Apache-2.0) already has pure-Go ModernBERT with arm64 GEMM assembly (**Unverified** quality) |
| ONNX Runtime via purego ([shota3506/onnxruntime-purego](https://github.com/shota3506/onnxruntime-purego), [amikos-tech/pure-onnx](https://github.com/amikos-tech/pure-onnx)) | no | CPU; CoreML EP possible | yes (MLAS AVX2/AVX-512; DirectML) | yes | CoreML EP, **Unverified** for this graph | yes: ORT library (~20–30 MB per platform, **Estimate**), signed with the app | Needs an ONNX export. The purego bindings are young ("currently unstable"; v0.0.1) |
| ONNX Runtime via cgo ([yalue/onnxruntime_go](https://github.com/yalue/onnxruntime_go) v1.36.0, or Microsoft's new official `github.com/microsoft/onnxruntime/go`) | **yes** | yes | yes, but needs MinGW | yes | CoreML EP | yes | Mature (yalue), but cgo breaks the Windows cross-build (see [Cross-compilation](#cross-compilation-and-releases)) |
| [GoMLX](https://github.com/gomlx/gomlx) | pure Go backend: no; XLA: yes | Go backend: no NEON; XLA CPU | Go backend (AVX2 only with GOEXPERIMENT=simd); no XLA | XLA CPU | no (CoreML backend "currently broken") | XLA: PJRT plugin | ModernBERT not supported by its HF loader; ONNX import op coverage **Unverified** |
| [hugot](https://github.com/knights-analytics/hugot) | Go backend no; ORT/XLA yes (plus the Rust tokenizer via cgo) | builds | builds | tested | via ORT | ORT | Pipelines library; "only built/tested on amd64-linux"; no ModernBERT evidence |
| Metal via MLX ([mlx-c](https://github.com/ml-explore/mlx-c), cgo) | yes, darwin only | GPU | n/a | n/a | yes | the MLX library and its `.metallib` | [laya-mlx](https://github.com/mizorewww/laya-mlx) shows the gain (below). The only Go bindings are tiny community projects |
| Metal via MPS/MPSGraph through purego's `objc` package | no | GPU | n/a | n/a | yes | no | Most work; no dependency. **Unverified** feasibility |

What other ports report. These are their own figures, not re-measured:

- **[@receptron/laya](https://github.com/receptron/laya)** (Node.js, ONNX Runtime, MIT):
  - fp32 ONNX export: opset 18, dynamo, about 1e-5 max logit difference
  - "about 140 ms" for three questions on an Apple Silicon CPU
  - about 2 GB RAM
  - Its export script notes that `nn.TransformerEncoderLayer`'s fused fast path can't be exported,
    so it exports with gradients enabled.
- **[MstyAI/laya-onnx](https://github.com/MstyAI/laya-onnx)** (Go, Apache-2.0). This is already a
  Go port:
  - fp16 ONNX
  - ONNX Runtime 1.23.2 through shota3506/onnxruntime-purego, with the library downloaded and
    checked by sha256
  - gomlx/go-huggingface's pure-Go tokenizer
  - Reports a 359–397 ms warm p50 on an M3 Max and 3.1–3.7 GiB of memory.

  It is the fastest route to "no Python" and a useful baseline for phase 0. Its memory use and
  the fp16-on-CPU choice are why this design doesn't adopt it as-is.
- **[laya-mlx](https://github.com/mizorewww/laya-mlx)** (Python, MLX, Apache-2.0), on an M3 Max
  with fp16:
  - 13.4 ms for one short question, against 22.7 ms for PyTorch MPS fp32
  - 146.8 questions/s at 50 questions
  - 944 MiB peak memory
  - Naive 8-bit and 4-bit quantization "failed to accelerate the larger pilot workloads, and
    changed predictions or calibrated probabilities".

  Its demo is Snake. The "up to 50 times faster" and Tetris claims quoted in
  [laya-performance.md](laya-performance.md) come from press and social posts comparing it with
  Jev's hosted API, not from the repo.
- **Others** listed on Hugging Face and GitHub but not examined:
  - Rust/candle ports (several named `laya-rs`, and `laya-candle` with Metal kernels)
  - a Zig port
  - CoreML, LiteRT and GGUF conversions
  - several ONNX exports:
    - fp32 exports of all three checkpoints (`mariojcr/laya-onnx`)
    - a weight-only int8 export (`inferenceprince/laya-onnx-int8`, 606 MB)

### Recommendation

1. **Build the native engine in Go.** It is the portable reference, and on macOS, with Accelerate
   kernels through purego, it is already as fast as PyTorch on the CPU, with no cgo and nothing to
   ship. Tokenizer, prompt building, calibration, server and cache are Go in every configuration.
2. **Make Windows and Linux fast in phase 5, chosen by benchmark.** Either write SIMD kernels, which
   keep zero dependencies, or add an ONNX Runtime engine through purego, which is faster to reach
   and adds GPU providers such as DirectML but ships a DLL. Measure both against a 780-token batch
   before choosing.
3. **Add a Metal engine for Macs in phase 5.** It is the only way to match or beat today's MPS
   latency. Spike MLX through cgo (darwin builds already use cgo for Wails) against MPSGraph
   through purego.

A whole-model `Engine` interface keeps all three possible without changing callers.

## Speed estimate for the Go CPU engines

The work per token is about 368M multiply-adds through the linears (0.74 GFLOP):

- Encoder: 12.26M per token per layer × 28 = 343M
- Head: 12.6M × 2 = 25M

Attention adds little at game sizes. So compute grows with the number of tokens in a call.

Measured on this Mac. The GEMM-only rows time the actual sequence of 120 matrix multiplies in one
Laya forward pass (28 × 4 encoder linears plus 2 × 4 head linears) with random weights:

| Measurement | 56 tokens (1 state) | 300 tokens (~6 states) | 780 tokens (~14 states) |
|---|---|---|---|
| Work | 41 GFLOP | 221 GFLOP | 575 GFLOP |
| Accelerate `cblas_sgemm` via purego, `CGO_ENABLED=0`, GEMMs only | **71 ms** | **203 ms** | **395 ms** |
| Plain Go (parallel, no SIMD), GEMMs only | 2.8 s | 11.3 s | 24.6 s |
| PyTorch CPU fp32, whole `predict_many` call (`laya_batch.py`) | 81 ms | 201 ms (6 states) | 431 ms (16 states) |
| PyTorch MPS, whole call ([laya-performance.md](laya-performance.md)) | 67–93 ms | 63–65 ms (Invaders) | 100–130 ms (Tetris) |

Single large multiplies reached 1.4 TFLOP/s (56 × 1024 by 1024 × 5248) and 2.5 TFLOP/s (896 rows)
through Accelerate. gonum's pure-Go `Sgemm` reached 26 GFLOP/s on the same shape. It has no arm64
assembly.

**Estimate** for a whole Go call, adding 10–20% for the non-GEMM ops once they are parallel:

| Engine on M4 Pro | 1 state | Space Invaders (6 states) | Tetris (~14 states) |
|---|---|---|---|
| Native + Accelerate | 80–90 ms | 220–250 ms | 430–480 ms |
| Native + NEON assembly (assumes 400–700 GFLOP/s) | 70–120 ms | 350–600 ms | 0.9–1.6 s |
| Native + plain Go | ~3 s | ~12 s | ~27 s |
| PyTorch MPS today (measured) | 67–93 ms | 63–65 ms | 100–130 ms |

**Estimate** for an 8-core AVX2 desktop with SIMD kernels or ONNX Runtime at 300–500 GFLOP/s:

- 1 state: about 100–150 ms
- Tetris batch: about 1.2–2 s

What this means for System 1 Arcade:

- **Lockstep and Tetris are fine on the Accelerate engine.** Tetris decides once per piece.
- **Real-time Space Invaders and Frogger would slow** from about 15 decisions a second to about 4.
  That is below the default 6 inputs a second, so the model would set the pace.
- **The answer cache offsets part of it**, since those games repeat sentences.
- **The Metal engine is what restores today's speed.**

Plain Go without SIMD is only good for correctness tests and occasional use.

Memory: Accelerate needs fp32, so the linears take 1.48 GB resident. Embeddings can stay fp16
(103 MB), with rows converted when gathered. About 1.6 GB in all, the same as PyTorch, which also
runs in fp32 on CPU and MPS. Converting the fp16 file to fp32 at load is 370M conversions: under a
second in parallel (**Estimate**).

## Tokenizer

Laya needs exactly one tokenizer operation: `encode(text) → ids` with no special tokens added.
Write it in the module, about 500 lines of Go reading `tokenizer.json`, rather than depend on a
general library. The traps below are where general ports tend to differ from Hugging Face's Rust
implementation.

The pipeline:

1. **Added tokens first.** The raw text is split on the 116 added tokens before anything else.
   These aren't only special tokens. They include:
   - 23 runs of 2–24 spaces
   - `|||IP_ADDRESS|||`, `|||EMAIL_ADDRESS|||` and `|||PHONE_NUMBER|||`
   - `[unused0]`…

   Examples measured with the Python tokenizer:
   - `"a  b    c"` → `a`, `"  "` (50276), `b`, `"    "` (50274), `c`, and not `Ġ` merges.
   - `"x [SEP] y"` → `x`, `Ġ`, **50282** (`[SEP]`), `Ġy`. A `[SEP]` typed into a state becomes the
     real separator.

   Non-special added tokens are matched on the normalized text. `[MASK]` has `lstrip: true`, but
   Laya replaces `[MASK]` in all text before tokenizing.
2. **NFC normalization:** `golang.org/x/text/unicode/norm`.
3. **ByteLevel pre-tokenizer** with the GPT-2 pattern
   `'s|'t|'re|'ve|'m|'ll|'d| ?\p{L}+| ?\p{N}+| ?[^\s\p{L}\p{N}]+|\s+(?!\S)|\s+`. Go's `regexp` (RE2)
   has no lookahead, so `\s+(?!\S)` needs a hand-written scanner. Each piece is mapped
   byte-to-unicode (`Ġ` for space, `Ċ` for newline).
4. **BPE** with 50,009 merge ranks. Cache the results per piece, since game prompts repeat words
   constantly.

Existing Go options, for comparison and cross-checking:

- [daulet/tokenizers](https://github.com/daulet/tokenizers) wraps the Rust library exactly, but
  through cgo and a static library with no Windows build.
- [sugarme/tokenizer](https://github.com/sugarme/tokenizer) (pure Go, low activity) and
  [gomlx/go-huggingface](https://github.com/gomlx/go-huggingface)'s `hftokenizer` (pure Go, used by
  MstyAI/laya-onnx and hugot) both load `tokenizer.json` with ByteLevel BPE and added tokens. Their
  byte-exact parity on this tokenizer is **Unverified**, and go-huggingface brings GoMLX into the
  module graph.
- [amikos-tech/pure-tokenizers](https://github.com/amikos-tech/pure-tokenizers) loads the Rust
  library through purego, but it downloads a native library.

Whichever is used, the gate is the same: identical ids on the golden corpus.

## Weights, formats and download

**Format.** Read `model.safetensors` directly: an 8-byte header length, a JSON header, then raw
little-endian tensors. The header of this file is 21,536 bytes. Memory-map the file on macOS and
Linux; on Windows, read it. Convert fp16 to fp32 for the Accelerate kernels
([x448/float16](https://github.com/x448/float16), MIT, or a 20-line bit-twiddling conversion).
No conversion step, and no second copy on disk. The same file works for Python and Go.

**Precision options:**

| Form | Download | Resident | Status |
|---|---|---|---|
| fp16, as shipped | 842.6 MB | — | the only format to download |
| fp32 compute (Accelerate, portable) | — | ~1.6 GB (embeddings kept fp16) | phase 1–2 |
| fp16 compute (Metal, and ORT on GPUs) | — | ~0.85 GB | phase 5. fp16 ONNX on CPUs without native fp16 was reported at ~3.9 s a call, so not for CPU |
| int8 weight-only, per channel or block of 64 | ~0.42–0.61 GB if distributed | ~0.5 GB | later, for memory only |

About int8:

- One ONNX conversion reports that **dynamic** int8, which quantizes activations too, "destroys this
  model" because ModernBERT has outlier activation channels: 69–77% argmax agreement. The same
  report gives 100% for weight-only int8.
- laya-mlx found 8-bit didn't speed up its larger workloads and changed probabilities.

With Accelerate, int8 weights would be expanded to fp32 before each multiply, so they save memory
but not time. Treat quantization as a memory option, gated by the same parity tests.

**Download and cache.** `laya/hub` fetches
`https://huggingface.co/<repo>/resolve/<revision>/<file>` for the five files above:

- **Pinned revision.** The default revision is pinned to the commit the release was tested
  against; `main` is allowed by option.
- **Integrity.** The `x-linked-etag` response header is the file's sha256, and it is checked after
  download.
- **Resumable.** Partial downloads resume from a `.incomplete` file.
- **Progress** goes to `Options.Progress`, so the app can show "Downloading Laya (412 of 804 MB)".
- **Python's cache layout.** `models--convaiinnovations--laya/{blobs, snapshots/<rev>, refs}` under
  `$HF_HUB_CACHE`, `$HF_HOME/hub` or `~/.cache/huggingface/hub`. A model the Python agent
  downloaded is found without downloading again.
- **Windows.** Snapshot entries on Windows are file copies instead of symlinks, which is what
  `huggingface_hub` does there when symlinks aren't available.
- **Tokens.** `HF_TOKEN` is sent only to `huggingface.co`.

go-huggingface's `hub` package implements the layout, but it ignores `HF_HOME` and `HF_HUB_CACHE`,
is "not yet fully supported" on Windows, and adds GoMLX to the module graph. Around 250 lines of our
own code is the better trade.

## Parity testing

**Fixtures come from the Python reference.**
`tools/golden/make_golden.py` pins `laya==0.3.6`, `torch==2.14.0`, `transformers==5.17.0`,
`tokenizers==0.23.2` and the checkpoint revision. It runs on the CPU in fp32 and writes JSONL, one
record per prompt:

- the request (state and questions)
- the token ids and marker positions per question
- the raw logits and act logits
- the answers exactly as `Agent.predict` and `predict_many` return them

For about 20 prompts it also writes intermediate tensors (`emb`, after layers 0, 1, 2, 3 and 27,
`final_norm`, both head layers) as safetensors, for layer-by-layer bisection with the `Trace` hook.
Fixtures are regenerated only when the Python reference version changes, and the version is
recorded in each file.

**Corpus:**

1. Real game prompts, captured from `GET /v1/laya` while `cmd/headless` plays each game with the
   oracle (all three games, a few seeds, several thousand prompts).
2. Laya's own presets (`triage_questions`, `email_questions`, `guard_questions`,
   `moderation_questions`, `router_questions`) with a handful of states each.
3. Edge cases:
   - states over 512 tokens (right truncation)
   - states of 66–200 tokens (the sliding window matters)
   - NFC and non-NFC Unicode, emoji, CJK
   - runs of spaces and tabs
   - `[SEP]`, `[MASK]` and `|||EMAIL_ADDRESS|||` inside states
   - JSON states with nested objects, non-ASCII text and key order that isn't sorted
   - dict-valued criteria, `None` and `""` descriptions
   - a one-option choice
   - 6–10 options and 11+ options (the clamped `choice:11+` temperature)
   - options that overflow `head_max_len` (must error the same way)
   - noul with custom `true`/`false` descriptions
   - 5-level scores
4. Tokenizer-only strings: a few thousand lines of mixed text, to check ids alone.

**Tolerances:**

| Check | fp32 engines (Accelerate, portable) | fp16 / int8 engines |
|---|---|---|
| Token ids and marker positions | identical | identical |
| Raw logits | max abs diff ≤ 1e-3 (expect ~1e-5, as receptron's fp32 ONNX did) | ≤ 5e-2 |
| Calibrated probabilities | max abs diff ≤ 5e-4 | ≤ 2e-2 |
| Answers rounded to 4 decimals | equal in ≥ 99% of fields; the rest differ by 1e-4 | — |
| Chosen option or noul > 0.5 | identical wherever the Python top-two gap is > 1e-3 | ≥ 99.5% agreement on game prompts |

Baseline: on this Mac, PyTorch's CPU and MPS runs gave identical 4-decimal answers on 21 game
prompts (7 states × 3 questions). Calling one state at a time and batching also gave identical
answers. So 4-decimal equality is realistic for fp32 engines.

**Game-level checks.** Play Tetris seeds 1–10 in lockstep (3,000-piece cap) with the Go engine and
with the Python server, and compare lines per seed. Equal answers give the same games; any
differences should come from near ties. Run `agents/eval_questions.py` against `laya-server` for
per-question agreement with the oracle.

**Benchmarks.** `go test -bench` in the module:

- tokenize
- 1 state with 1 question
- Space Invaders-shaped (6 states)
- Tetris-shaped (14 states)
- one 512-token state

Each reports ms/op, allocations and peak RSS, alongside the Python numbers above on the same
machine. Use `laya bench --engine … --threads …` to compare engines on a user's machine.

## Cross-compilation and releases

The app today:

- **macOS** builds need cgo anyway (Wails uses WKWebView).
- **Linux** builds need cgo (GTK and WebKitGTK).
- **Windows** builds use pure-Go WebView2 bindings, so the app currently cross-compiles for Windows
  from a Mac with `CGO_ENABLED=0`. That must keep working.

| Laya engine in the app | macOS build | Windows from a Mac | Linux build | `CGO_ENABLED=0` tools (`cmd/headless`, `laya-server`) | Ships native code |
|---|---|---|---|---|---|
| Native + Accelerate (darwin) / portable (others) | yes | yes | yes | yes | no |
| Native + SIMD assembly | yes | yes | yes | yes | no |
| ONNX Runtime via purego | yes | yes | yes | yes | yes: `libonnxruntime.dylib` in the `.app` (signed and notarized with it), `onnxruntime.dll` next to the `.exe` |
| ONNX Runtime via cgo | yes | **no**: needs a MinGW cross compiler (or `zig cc`) and the ORT headers | yes | no | yes |
| Metal via MLX (cgo) | yes | n/a (darwin only) | n/a | no | yes: MLX dylib and `.metallib` in the bundle (**Unverified** details) |

purego's Tier 1 platforms are macOS, Linux and Windows on amd64 and arm64, all with
`CGO_ENABLED=0`. It passes float arguments correctly on those platforms, and `cblas_sgemm` takes
two (`alpha`, `beta`). Engines that need cgo or native libraries should live behind build tags or
in a separate module, such as `github.com/brennanMKE/laya-go/ort`, so importing `laya` never pulls
them in.

Releases of `laya-go`:

- Tagged semver releases.
- Each release records the checkpoint revision and the Python `laya` version it was checked against.
- `laya-server` and `laya` binaries for darwin/arm64, darwin/amd64, windows/amd64, windows/arm64,
  linux/amd64 and linux/arm64, built with `CGO_ENABLED=0` from one machine.
- The weights are never embedded in the binaries (842 MB); they download on first use.

## Licensing

- **Laya weights and code: Apache-2.0.**
  - The model card's front matter says `license: apache-2.0`. So do `laya-multilingual` and
    `laya-typed-decisions`. None of the Hugging Face repos has a LICENSE file.
  - The GitHub repo [NandhaKishorM/laya](https://github.com/NandhaKishorM/laya) and the PyPI
    package are Apache-2.0 with a LICENSE file.
  - The card has a `commercial-use` tag and no other restrictions.
- **ModernBERT-large** ([answerdotai/ModernBERT-large](https://huggingface.co/answerdotai/ModernBERT-large)):
  Apache-2.0 on Hugging Face and GitHub.
- **mmBERT-base**, the multilingual checkpoint's encoder
  ([jhu-clsp/mmBERT-base](https://huggingface.co/jhu-clsp/mmBERT-base)): MIT on Hugging Face. The
  GitHub repo has no LICENSE file.
- **laya-go itself: Apache-2.0**, with a NOTICE file.
  - The prompt builder, option rendering, calibration and model definition are translations of
    Laya's Apache-2.0 Python, so they are derivative works.
  - The NOTICE credits Convai Innovations and NandhaKishorM/laya at the version ported (0.3.6),
    and changed files say so.
  - laya-mlx does the same.
- **System 1 Arcade (MIT) can import an Apache-2.0 module.** Its binaries must then include the
  Apache-2.0 text and laya-go's NOTICE, for example in a third-party notices file in the app bundle
  and installer.
- **Dependencies:**
  - purego: Apache-2.0
  - golang.org/x/text: BSD-3
  - x448/float16: MIT
  - ONNX Runtime, if used: MIT, plus its ThirdPartyNotices
- **Redistributing weights is allowed** under Apache-2.0: include the license, keep notices, and
  state any modifications, such as an int8 conversion. The plan still downloads from the upstream
  repo at a pinned revision and doesn't mirror anything, so there is nothing to redistribute at
  first.

This is a reading of the licenses, not legal advice.

## Plan

Each phase ends with something testable. Durations are rough, for one person.

**Phase 0: groundwork (about 1 week)**

- Create the repo and module.
- Write `make_golden.py`, and capture the game corpus with `cmd/headless`.
- Write the tokenizer, `build_sequence`, `render_options`, the Python-compatible JSON encoder, and
  calibration.
- Run MstyAI/laya-onnx and rembed on the corpus as reference points.
- *Milestone:* token ids and marker positions identical on the full corpus. Calibration reproduces
  Python's answers when given Python's logits.

**Phase 1: native engine, portable kernels (1–2 weeks)**

- Write the safetensors loader with shape checks, the ModernBERT forward pass with packed
  sequences, the head, scorer and act head, and the `Trace` hook.
- *Milestone:* every fixture within the fp32 tolerances. `laya predict` works end to end (slowly).

**Phase 2: Accelerate kernels and speed on macOS (about 1 week)**

- Accelerate `MatMulT` through purego.
- Parallel GELU, LayerNorm and attention, and a fast erf.
- Weight conversion at load, and benchmarks.
- *Milestone:* on the M4 Pro, 1 state ≤ 90 ms, 6 states ≤ 250 ms, 14 states ≤ 480 ms, peak RSS
  ≤ 1.8 GB, all fixtures still passing.

**Phase 3: hub, answer cache and server (about 1 week)**

- `laya/hub` with progress and a shared cache.
- `laya/answercache`.
- `laya/wire` with ordered decoding.
- `cmd/laya-server`.
- *Milestone:* `laya-server` passes the same HTTP tests as `laya_server.py`.
  `agents/laya_agent.py --policy sidecar` and `eval_questions.py` run against it. The game-level
  Tetris check matches.

**Phase 4: adopt in System 1 Arcade (about 1 week).** See [Adoption](#adopting-it-in-system-1-arcade).

- *Milestone:* the app plays all three games with no Python installed. `wails build -platform
  windows/amd64` still works from a Mac.

**Phase 5: beyond CPU parity (spikes, then pick)**

- A Metal engine: MLX through cgo or MPSGraph through purego. *Target:* at or below today's MPS
  times (Invaders ≤ 65 ms, Tetris ≤ 130 ms).
- For Windows and Linux, SIMD kernels or an ONNX Runtime engine through purego, whichever wins a
  780-token benchmark on an AVX2 machine by enough to pay for its cost.

**Phase 6: more checkpoints and features**

- `typed-decisions`: same architecture; `max_len` 1024 and `head_max_len` 256.
- `multilingual`: an mmBERT-base encoder with 22 layers, hidden 768 and RoPE θ 160000 for both
  layer types; a 256k-vocabulary tokenizer (a 34 MB `tokenizer.json`) whose pipeline must be
  checked separately; a 644 MB checkpoint.
- The `Router` with the language detection from `lang.py`.
- The embedding shortlist.
- Optional int8 weights.

## Adopting it in System 1 Arcade

**Interim step: no Go changes to the agent loop.** Ship `laya-server` inside the app and have
`agentManager.launchServer` start it instead of Python. It prints the same `agent ready` line. This
removes Python right away with a small diff in `agent.go`:

- drop `layaPython`, `basePython`, `hasLaya` and the venv setup
- drop the `//go:embed agents/laya_server.py agents/laya_batch.py`

**Final step: run the model in the app's process.**

- **The agent loop takes an interface.** In `internal/agent`, add
  `type Asker interface { Ask(ctx, prompt map[string]any) (game.Answers, error) }`. `Client` (HTTP)
  already has that method. `Run` takes an `Asker`.
- **A new `internal/agent/local.go` holds the built-in agent.** It:
  - owns a `*laya.Model` and a Go answer cache that honors `SYSTEM1_LAYA_CACHE`
  - converts `game.Prompt` to `laya.Prompt`, with criteria sorted by name as today's JSON does
  - calls `PredictMany` once per decision
  - maps answers to `game.Answer` under `"<key>.<question>"`
- **`agentManager.start` loads the model instead of launching a server.** For the built-in agent it
  calls `laya.Load` with a `Progress` callback that feeds the existing status line ("Downloading
  the Laya model (412 of 804 MB)…", then "Loading the Laya model…"). The model stays loaded while
  the agent runs; `stop` closes it to give back its ~1.6 GB.
- **`cmd/headless` can use the same local agent**, which makes Go-only benchmarks and CI runs
  possible.
- **Remove:**
  - the Python requirement in the README
  - the `--agent` / `-Agent` venv step in `scripts/build.sh` and `scripts/build.ps1`
  - the "downloads Laya and PyTorch (about 1 GB) and needs Python 3.10" known issue

  `agents/laya_server.py`, `laya_agent.py` and `eval_questions.py` stay as development tools and
  as an example of an external agent. The Python server remains usable as a custom agent, which
  keeps MPS speed available until the Metal engine exists.
- **Speed.** On Macs the built-in agent would run at PyTorch-CPU speed until phase 5. Measure all
  three games in real time with the answer cache on before switching the default. If Space
  Invaders or Frogger scores drop, keep the Python/MPS server as the default on macOS until the
  Metal engine lands.

## Risks and open questions

- **CPU speed may not be enough for real-time play.** The Accelerate engine is 2–4 times slower
  than MPS for multi-state decisions (above). Plain Go is 40–60 times slower than Accelerate. The
  Metal engine or SIMD kernels are needed, not optional, for a good experience everywhere.
- **Windows speed.** Nothing has been measured on Windows. The estimate assumes AVX2 kernels or ONNX
  Runtime; without either, Windows is unusable in real time.
- **Tokenizer drift.** The added-token splitting, the lookahead-free pre-tokenizer and NFC are easy
  to get subtly wrong. Only the corpus test protects against it, so the corpus has to be broad.
- **Upstream changes.** Laya changes prompt construction and calibration between releases. 0.3.x
  added the temperature clamp and `render_criterion`, for example. Pin the Python version and
  checkpoint revision per laya-go release, regenerate fixtures when either changes, and record both
  in `Info()`.
- **Calibration behavior.** The clamp to [0.5, 5.0] is a Python package decision, not part of the
  checkpoint. Laya's card says the checkpoint "ships over-confident" and recommends refitting
  temperatures. Match Python by default, and let callers pass their own temperatures.
- **Memory.** ~1.6 GB in the app process instead of a separate one. Is that acceptable on 8 GB
  machines, and should the model unload after a period without use?
- **Young dependencies.** purego is "beta software" (v0.11.1); the purego ONNX Runtime bindings are
  unstable or v0.0.1; Go 1.27's `simd` package is experimental. The Accelerate path uses one
  purego call (`Dlopen` plus one function), which limits the exposure.
- **Metal engine choice.** MLX through cgo is proven by laya-mlx but adds a native library and its
  shaders to the bundle; MPSGraph through purego has no dependency but is more work and unproven.
  A spike should decide.
- **ONNX export.** If the ORT engine is chosen, use an existing export (receptron's fp32 export, or
  our own with its script) or run the export ourselves? The export depends on the transformers
  version: recent releases had ModernBERT export bugs, such as
  [transformers#45735](https://github.com/huggingface/transformers/issues/45735).
- **Name and home.** `github.com/brennanMKE/laya-go` is a placeholder. Ask the Laya authors whether
  they would link it, or host it under their organization.
- **Adopt MstyAI/laya-onnx instead?** It already exists in Go. Its reported speed and memory are
  worse than this design's estimate, and it depends on a downloaded ORT library. Phase 0's
  benchmark should confirm or overturn that before we build our own.

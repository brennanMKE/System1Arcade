"""Answer many (state, questions) prompts in one Laya forward pass.

laya's Agent.predict batches the questions of a single state. Games that
split a decision into several short states (one sentence per Tetris landing
spot) would otherwise pay one forward pass per state. This mirrors
Agent.system_one from laya 0.3.x but collates every prompt into one batch.

answer_cached puts an LRU of answers in front of either, so repeated
(state, question) pairs skip the model.
"""

import json
import sys
from collections import OrderedDict

import numpy as np
import torch
from laya.common import QTYPES, build_sequence, collate_items, confidence_from_probs, render_options, temp_bucket


@torch.no_grad()
def predict_many(agent, prompts):
    """prompts: {key: {"state": ..., "questions": {...}}} -> {key: {question: answer}}"""
    max_len = agent.cfg.get("max_len", 512)
    head_max_len = agent.cfg.get("head_max_len", 192)
    items, index = [], []
    for key, p in prompts.items():
        for qid, qdef in p["questions"].items():
            q = agent._to_internal(qdef)
            seq, markers = build_sequence(agent.tok, p["state"], q, max_len, head_max_len)
            if len(markers) != len(render_options(q)):
                raise ValueError(f"question {key}.{qid} options exceed head_max_len={head_max_len}")
            items.append({"ids": seq, "markers": markers, "qtype": QTYPES[q["t"]]})
            index.append((key, qid, q))
    if not items:
        return {}
    b = collate_items([items], agent.tok.pad_token_id)
    dev = agent.device
    with torch.autocast(device_type=dev.type, dtype=agent.dtype, enabled=dev.type == "cuda"):
        logits, _ = agent.model(b["input_ids"].to(dev), b["attention_mask"].to(dev), b["marker_pos"].to(dev),
                                b["marker_mask"].to(dev), b["qtype"].to(dev))
    logits = logits.float().cpu().numpy()
    out = {}
    for r, (key, qid, q) in enumerate(index):
        k = len(items[r]["markers"])
        qt = QTYPES[q["t"]]
        z = logits[r, :k] / agent.temperature_by_options.get(temp_bucket(qt, k), agent.temperature[qt])
        p = np.exp(z - z.max())
        p = p / p.sum()
        if q["t"] == "choice":
            keys = list(q["crit"].keys())
            ans = {"type": "choice", "choice": keys[int(p.argmax())],
                   "probabilities": {kk: round(float(v), 4) for kk, v in zip(keys, p)},
                   "confidence": round(confidence_from_probs(p, k), 4)}
        elif q["t"] == "score":
            ans = {"type": "score", "score": round(float((np.arange(k) * p).sum()), 4),
                   "probabilities": {str(i): round(float(v), 4) for i, v in enumerate(p)}}
        else:
            ans = {"type": "noul", "noul": round(float(p[1]), 4), "confidence": round(max(float(p[1]), 1 - float(p[1])), 4)}
        out.setdefault(key, {})[qid] = ans
    return out


# --- Answer cache ---------------------------------------------------------
#
# Laya is deterministic: the same state and question always get the same
# answer. Frogger and Space Invaders repeat the same sentences many times
# ("No bomb is falling toward the cannon."), so answers are cached by
# (exact state text, question) and only the misses go to the model, still as
# one batch.

DEFAULT_CACHE_SIZE = 10000
CACHE_ENV = "SYSTEM1_LAYA_CACHE"  # number of answers to keep; 0 turns the cache off


def cache_size_from_env(default=DEFAULT_CACHE_SIZE):
    import os
    v = os.environ.get(CACHE_ENV, "").strip()
    return int(v) if v else default


def _text(v):
    return v if isinstance(v, str) else json.dumps(v, ensure_ascii=False)


def cache_key(state, qdef):
    """The exact state plus the question's type, instructions and criteria.

    The question's name is left out, so the same question asked under two
    names shares an answer. Criteria keep their order, because the order of
    the options is part of what the model reads.
    """
    crit = qdef.get("criteria")
    if isinstance(crit, dict):
        crit = [[k, v] for k, v in crit.items()]
    return json.dumps([_text(state), qdef["type"], _text(qdef["instructions"]), crit], ensure_ascii=False)


class AnswerCache:
    """A bounded LRU of answers, with hit and miss counts per question."""

    def __init__(self, size=DEFAULT_CACHE_SIZE):
        self.size = size
        self.entries = OrderedDict()
        self.hits = self.misses = self.model_calls = self.skipped_calls = 0

    def get(self, key):
        ans = self.entries.get(key)
        if ans is not None:
            self.entries.move_to_end(key)
        return ans

    def put(self, key, ans):
        self.entries[key] = ans
        self.entries.move_to_end(key)
        while len(self.entries) > self.size:
            self.entries.popitem(last=False)

    def stats(self):
        asked = self.hits + self.misses
        return {"enabled": self.size > 0, "size": len(self.entries), "capacity": self.size,
                "hits": self.hits, "misses": self.misses,
                "hit_rate": round(self.hits / asked, 4) if asked else 0.0,
                "model_calls": self.model_calls, "skipped_calls": self.skipped_calls}

    def summary(self):
        s = self.stats()
        return (f"cache {s['hits']} hits, {s['misses']} misses ({s['hit_rate']:.1%}), "
                f"{s['skipped_calls']} of {s['model_calls'] + s['skipped_calls']} model calls skipped")


def answer_cached(prompts, run, cache=None):
    """Answer {key: {"state", "questions"}} prompts, sending only cache misses to run.

    run(prompts) -> {key: {question: answer}} answers the misses in one call
    (predict_many, or Agent.predict for a single state). Repeated
    (state, question) pairs within the request are asked once.
    """
    if cache is None or cache.size <= 0:
        return run(prompts)
    keys = {(key, qid): cache_key(p["state"], qdef) for key, p in prompts.items() for qid, qdef in p["questions"].items()}
    found, pending, missing = {}, {}, {}  # found: ck -> answer; pending: ck -> (key, qid) asked
    for key, p in prompts.items():
        for qid, qdef in p["questions"].items():
            ck = keys[key, qid]
            if ck in found or ck in pending:
                cache.hits += 1  # answered by another item in this request
                continue
            ans = cache.get(ck)
            if ans is not None:
                cache.hits += 1
                found[ck] = ans
            else:
                cache.misses += 1
                pending[ck] = (key, qid)
                missing.setdefault(key, {"state": p["state"], "questions": {}})["questions"][qid] = qdef
    if missing:
        cache.model_calls += 1
        got = run(missing)
        for ck, (key, qid) in pending.items():
            found[ck] = got[key][qid]
            cache.put(ck, found[ck])
    else:
        cache.skipped_calls += 1
    return {key: {qid: found[keys[key, qid]] for qid in p["questions"]} for key, p in prompts.items()}


# --- Loading a checkpoint, optionally with tuned weights --------------------
#
# agents/finetune.py writes a small directory: tuned.safetensors holds only the
# tensors it trained (the decision head on top of the frozen encoder), and
# tuned.json says which base checkpoint they belong to. load_model applies
# them on top of the base model, so the 1.6 GB base download is shared and a
# tuned checkpoint is about 100 MB.

MODEL_ENV = "SYSTEM1_LAYA_MODEL"      # base checkpoint (default convaiinnovations/laya)
WEIGHTS_ENV = "SYSTEM1_LAYA_WEIGHTS"  # a directory written by agents/finetune.py
TUNED_TENSORS, TUNED_META = "tuned.safetensors", "tuned.json"


def apply_tuned(agent, weights_dir):
    """Copy tuned tensors from weights_dir into a loaded laya Agent, in place."""
    import os
    from safetensors.torch import load_file

    with open(os.path.join(weights_dir, TUNED_META)) as f:
        meta = json.load(f)
    tensors = load_file(os.path.join(weights_dir, TUNED_TENSORS))
    params = dict(agent.model.named_parameters())
    for name, t in tensors.items():
        if name not in params:
            raise ValueError(f"{weights_dir}: tensor {name!r} is not in the base model")
        if tuple(t.shape) != tuple(params[name].shape):
            raise ValueError(f"{weights_dir}: {name!r} has shape {tuple(t.shape)}, the base model {tuple(params[name].shape)}")
    with torch.no_grad():
        for name, t in tensors.items():
            params[name].copy_(t.to(params[name].dtype))
    agent.tuned = meta
    return agent


def load_model(model=None, weights=None, device=None):
    """laya.load(model) plus tuned weights from `weights` or $SYSTEM1_LAYA_WEIGHTS, if either is set."""
    import os
    import laya

    agent = laya.load(model or os.environ.get(MODEL_ENV) or "convaiinnovations/laya", device=device)
    weights = weights or os.environ.get(WEIGHTS_ENV, "").strip()
    if weights:
        apply_tuned(agent, os.path.expanduser(weights))
        print(f"laya: applied tuned weights from {weights}", file=sys.stderr, flush=True)
    return agent

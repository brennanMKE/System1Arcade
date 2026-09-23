"""Answer many (state, questions) prompts in one Laya forward pass.

laya's Agent.predict batches the questions of a single state. Games that
split a decision into several short states (one sentence per Tetris landing
spot) would otherwise pay one forward pass per state. This mirrors
Agent.system_one from laya 0.3.x but collates every prompt into one batch.
"""

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

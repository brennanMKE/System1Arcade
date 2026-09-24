#!/usr/bin/env python3
"""Fine-tune Laya's decision head on oracle-labelled game prompts.

The data comes from agents/oracle_selfplay.py: one JSONL line per distinct
(state, question) prompt, with the oracle's labels and the seeds it came from.

Only the decision head is trained (the 2 transformer layers, the question-type
embedding and the scorer, about 26M of 421M parameters). The ModernBERT encoder
stays frozen, so its output for each prompt is computed once and training is
seconds, not hours. Two things keep the head close to the base model:

  * an L2 pull toward the base head's weights (--anchor-l2), and
  * anchor prompts (--anchor, e.g. Tetris) whose targets are the base model's
    own answers, so games that are not being tuned keep their answers.

Evaluation reports, base vs tuned: accuracy on the training prompts, on
prompts from held-out seeds, on held-out sentences (a share of distinct
prompts kept out of training entirely), on a few hand-written paraphrases,
the smallest probability given to the right answer (the margin), and how far
anchor answers moved.

    .venv/bin/python agents/finetune.py --data '.laya-tuned/data/frogger-*.jsonl' \\
        --data '.laya-tuned/data/invaders-*.jsonl' --anchor .laya-tuned/data/anchor-tetris.jsonl \\
        --test-seeds 31-40 --out .laya-tuned/head-v1

The output directory holds tuned.safetensors (the head) and tuned.json (what
it was trained on and the results). Load it with --weights on laya_server.py
or laya_agent.py, or SYSTEM1_LAYA_WEIGHTS.
"""
import argparse
import glob
import hashlib
import json
import os
import random
import sys
import time

sys.path.insert(0, __file__.rsplit("/", 1)[0])

HEAD_PREFIXES = ("head.", "type_emb.", "scorer.")

# Sentences the games have not produced, in their style, to check the head
# still reads the words rather than remembering exact sentences.
SAFE = {"type": "choice", "instructions": "What kind of place is it?",
        "criteria": {"safe": "a safe place", "deadly": "a deadly place"}}
AIM = {"type": "choice", "instructions": "Where is the target?",
       "criteria": {"left": "to the left", "right": "to the right", "here": "lined up with the cannon"}}
THREAT = {"type": "noul", "instructions": "Is a bomb about to hit the cannon?"}
LEFT = {"type": "noul", "instructions": "Is it safe to move left?"}
PROBES = [
    ("Hopping up lands in a deadly place: a square a truck will drive through in 0.7 seconds, so the frog would be hit.", SAFE, "deadly"),
    ("Hopping left lands in a safe place: turtles the frog can ride safely.", SAFE, "safe"),
    ("Hopping down lands in a deadly place: open water, so the frog would drown.", SAFE, "deadly"),
    ("Staying put keeps the frog in a deadly place: a log that carries the frog off the screen in 0.2 seconds, so the frog would die.", SAFE, "deadly"),
    ("Hopping right lands in a safe place: the middle strip, which is safe ground.", SAFE, "safe"),
    ("Hopping up lands in a deadly place: a home that is already filled.", SAFE, "deadly"),
    ("The target is a little to the left of the cannon.", AIM, "left"),
    ("The target is slightly to the right of the cannon.", AIM, "right"),
    ("The cannon is directly under its target.", AIM, "here"),
    ("A bomb is falling straight at the cannon and will hit it in 0.2 seconds.", THREAT, "yes"),
    ("A bomb is falling, but it will miss the cannon.", THREAT, "no"),
    ("The space to the left of the cannon is clear.", LEFT, "yes"),
    ("The wall is to the left of the cannon.", LEFT, "no"),
]


def seed_list(spec):
    out = []
    for part in (spec or "").split(","):
        if part:
            a, _, b = part.partition("-")
            out += list(range(int(a), int(b or a) + 1))
    return set(out)


def load_records(patterns):
    """Merge JSONL files from oracle_selfplay.py by exact (state, question)."""
    merged = {}
    for pat in patterns:
        files = sorted(glob.glob(pat))
        if not files:
            sys.exit(f"no files match {pat}")
        for path in files:
            with open(path) as f:
                for line in f:
                    r = json.loads(line)
                    k = json.dumps([r["state"], r["question"]], sort_keys=True, ensure_ascii=False)
                    m = merged.setdefault(k, {"game": r["game"], "group": r["group"], "state": r["state"],
                                              "question": r["question"], "labels": {}, "count": 0, "seeds": set()})
                    for lab, n in r["labels"].items():
                        m["labels"][lab] = m["labels"].get(lab, 0) + n
                    m["count"] += r["count"]
                    m["seeds"].update(r["seeds"])
    return list(merged.values())


def option_keys(qdef):
    """Label names in the model's option order (noul is [no, yes])."""
    if qdef["type"] == "noul":
        return ["no", "yes"]
    crit = qdef["criteria"]
    return list(crit) if isinstance(crit, (dict, list)) else []


def held_out(rec, frac):
    """A stable share of distinct prompts that training never sees."""
    h = hashlib.sha1((rec["state"] + json.dumps(rec["question"], sort_keys=True)).encode()).digest()
    return h[0] / 256 < frac


def encode_items(agent, recs):
    """Frozen encoder output for each prompt, kept on the CPU."""
    import torch
    from laya.common import QTYPES, build_sequence, collate_items

    max_len, head_max_len = agent.cfg.get("max_len", 512), agent.cfg.get("head_max_len", 192)
    items = []
    for r in recs:
        q = agent._to_internal(r["question"])
        seq, markers = build_sequence(agent.tok, r["state"], q, max_len, head_max_len)
        items.append({"ids": seq, "markers": markers, "qtype": QTYPES[q["t"]]})
    out = []
    enc = agent.model.encoder
    with torch.no_grad():
        for i in range(0, len(items), 32):
            b = collate_items([items[i:i + 32]], agent.tok.pad_token_id)
            h = enc(input_ids=b["input_ids"].to(agent.device), attention_mask=b["attention_mask"].to(agent.device)).last_hidden_state
            for j, it in enumerate(items[i:i + 32]):
                n = len(it["ids"])
                out.append({"h": h[j, :n].float().cpu(), "markers": it["markers"], "qtype": it["qtype"]})
    return out


def head_logits(model, batch, device):
    """DecisionModel.forward after the encoder: type embedding, head layers, scorer."""
    import torch

    n, L = len(batch), max(b["h"].shape[0] for b in batch)
    k = max(len(b["markers"]) for b in batch)
    d = batch[0]["h"].shape[1]
    h = torch.zeros(n, L, d)
    att = torch.zeros(n, L, dtype=torch.bool)
    mpos = torch.zeros(n, k, dtype=torch.long)
    mmask = torch.zeros(n, k, dtype=torch.bool)
    for i, b in enumerate(batch):
        h[i, :b["h"].shape[0]] = b["h"]
        att[i, :b["h"].shape[0]] = True
        mpos[i, :len(b["markers"])] = torch.tensor(b["markers"])
        mmask[i, :len(b["markers"])] = True
    h, att, mpos, mmask = h.to(device), att.to(device), mpos.to(device), mmask.to(device)
    qtype = torch.tensor([b["qtype"] for b in batch], device=device)
    h = h + model.type_emb(qtype)[:, None, :]
    for layer in model.head.layers:
        h = layer(h, src_key_padding_mask=~att)
    m = torch.gather(h, 1, mpos[:, :, None].expand(-1, -1, h.size(-1)))
    return model.scorer(m).squeeze(-1).float().masked_fill(~mmask, -1e4)


def temperatures(agent, batch, device):
    import torch
    from laya.common import temp_bucket

    return torch.tensor([agent.temperature_by_options.get(temp_bucket(b["qtype"], len(b["markers"])),
                                                          agent.temperature[b["qtype"]]) for b in batch], device=device)


def probs(agent, feats, device):
    import torch

    agent.model.eval()
    out = []
    with torch.no_grad():
        for i in range(0, len(feats), 64):
            batch = feats[i:i + 64]
            z = head_logits(agent.model, batch, device) / temperatures(agent, batch, device)[:, None]
            p = torch.softmax(z, -1).cpu()
            out += [p[j, :len(b["markers"])].tolist() for j, b in enumerate(batch)]
    return out


def score(recs, ps):
    """(accuracy, smallest probability of the right label) over records with a target label."""
    right, low = 0, 1.0
    for r, p in zip(recs, ps):
        keys = option_keys(r["question"])
        want = keys.index(r["target"])
        right += max(range(len(p)), key=p.__getitem__) == want
        low = min(low, p[want])
    return (right / len(recs) if recs else float("nan")), low


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--data", action="append", required=True, help="JSONL glob from oracle_selfplay.py (repeatable)")
    ap.add_argument("--anchor", action="append", default=[], help="JSONL glob whose prompts keep the base model's answers")
    ap.add_argument("--test-seeds", default="", help="prompts seen only in these seeds are test data, e.g. 31-40")
    ap.add_argument("--holdout", type=float, default=0.2, help="share of distinct prompts held out of training")
    ap.add_argument("--min-purity", type=float, default=0.98, help="drop prompts whose oracle label is less consistent")
    ap.add_argument("--epochs", type=int, default=40)
    ap.add_argument("--lr", type=float, default=1e-4)
    ap.add_argument("--anchor-l2", type=float, default=1e-4, help="pull toward the base head's weights")
    ap.add_argument("--anchor-weight", type=float, default=1.0, help="loss weight of anchor prompts")
    ap.add_argument("--model", default=None, help="base checkpoint (default convaiinnovations/laya)")
    ap.add_argument("--device", default=None)
    ap.add_argument("--seed", type=int, default=0)
    ap.add_argument("--out", required=True, help="directory for tuned.safetensors and tuned.json")
    args = ap.parse_args()

    import torch
    from safetensors.torch import save_file

    import laya
    from laya_batch import TUNED_META, TUNED_TENSORS, load_model

    random.seed(args.seed)
    torch.manual_seed(args.seed)
    t_start = time.time()

    test_seeds = seed_list(args.test_seeds)
    recs, dropped = [], 0
    for r in load_records(args.data):
        top, n = max(r["labels"].items(), key=lambda kv: kv[1])
        if n / sum(r["labels"].values()) < args.min_purity or top not in option_keys(r["question"]):
            dropped += 1  # the sentence doesn't determine the label (e.g. "open space on both sides")
            continue
        r["target"] = top
        r["split"] = ("heldout" if held_out(r, args.holdout) else
                      "test" if r["seeds"] and r["seeds"] <= test_seeds else "train")
        recs.append(r)
    anchors = load_records(args.anchor) if args.anchor else []
    probes = [{"game": "probe", "group": "probe", "state": s, "question": q, "target": t} for s, q, t in PROBES]
    split = {s: [r for r in recs if r["split"] == s] for s in ("train", "test", "heldout")}
    print(f"{len(recs)} prompts ({', '.join(f'{len(v)} {k}' for k, v in split.items())}), "
          f"{dropped} dropped as ambiguous, {len(anchors)} anchor prompts, {len(probes)} probes", flush=True)

    agent = load_model(args.model, device=args.device)
    dev = agent.device
    feats = {k: encode_items(agent, v) for k, v in {**split, "anchor": anchors, "probe": probes}.items()}
    print(f"encoded in {time.time() - t_start:.0f}s", flush=True)

    def report(tag):
        res = {}
        for k in ("train", "test", "heldout", "probe"):
            if split.get(k, probes if k == "probe" else None):
                recs_k = split[k] if k != "probe" else probes
                acc, low = score(recs_k, probs(agent, feats[k], dev))
                res[k] = {"n": len(recs_k), "accuracy": round(acc, 4), "min_p_right": round(low, 4)}
        if anchors:
            now = probs(agent, feats["anchor"], dev)
            res["anchor"] = {"n": len(anchors), "max_abs_change": round(max(
                max(abs(a - b) for a, b in zip(p, q)) for p, q in zip(now, anchor_targets)), 4),
                "top_changed": sum(max(range(len(p)), key=p.__getitem__) != max(range(len(q)), key=q.__getitem__)
                                   for p, q in zip(now, anchor_targets))}
        print(tag, json.dumps(res), flush=True)
        return res

    anchor_targets = probs(agent, feats["anchor"], dev) if anchors else []
    before = report("base ")

    # Training set: oracle one-hot targets, plus anchors with the base model's own answers.
    train = []
    for r, f in zip(split["train"], feats["train"]):
        t = [0.0] * len(f["markers"])
        t[option_keys(r["question"]).index(r["target"])] = 1.0
        train.append((f, t, 1.0))
    train += [(f, t, args.anchor_weight) for f, t in zip(feats["anchor"], anchor_targets)]

    model = agent.model
    for p in model.parameters():
        p.requires_grad_(False)
    trained = {n: p for n, p in model.named_parameters() if n.startswith(HEAD_PREFIXES)}
    for p in trained.values():
        p.requires_grad_(True)
    base = {n: p.detach().clone() for n, p in trained.items()}
    opt = torch.optim.AdamW(trained.values(), lr=args.lr, weight_decay=0.0)
    print(f"training {sum(p.numel() for p in trained.values()) / 1e6:.1f}M head parameters "
          f"on {len(train)} prompts for {args.epochs} epochs", flush=True)
    t_train = time.time()
    for epoch in range(args.epochs):
        model.train()
        random.shuffle(train)
        total = 0.0
        for i in range(0, len(train), 32):
            chunk = train[i:i + 32]
            batch = [c[0] for c in chunk]
            z = head_logits(model, batch, dev) / temperatures(agent, batch, dev)[:, None]
            logp = torch.log_softmax(z, -1)
            tgt = torch.zeros_like(logp)
            for j, (_, t, _) in enumerate(chunk):
                tgt[j, :len(t)] = torch.tensor(t)
            w = torch.tensor([c[2] for c in chunk], device=dev)
            loss = (-(tgt * logp.clamp_min(-1e3)).sum(-1) * w).sum() / w.sum()
            loss = loss + args.anchor_l2 * sum(((p - base[n]) ** 2).sum() for n, p in trained.items())
            opt.zero_grad()
            loss.backward()
            opt.step()
            total += float(loss.detach()) * len(chunk)
        if epoch % 10 == 9 or epoch == args.epochs - 1:
            print(f"epoch {epoch + 1}: loss {total / len(train):.4f}", flush=True)
    train_s = time.time() - t_train
    model.eval()
    after = report("tuned")

    os.makedirs(args.out, exist_ok=True)
    save_file({n: p.detach().float().cpu().contiguous() for n, p in trained.items()}, os.path.join(args.out, TUNED_TENSORS))
    meta = {"base_model": args.model or "convaiinnovations/laya", "laya_version": laya.__version__,
            "trained": list(HEAD_PREFIXES), "data": args.data, "anchor": args.anchor,
            "prompts": {k: len(v) for k, v in split.items()}, "anchor_prompts": len(anchors), "dropped": dropped,
            "games": sorted({r["game"] for r in recs}), "test_seeds": args.test_seeds, "holdout": args.holdout,
            "epochs": args.epochs, "lr": args.lr, "anchor_l2": args.anchor_l2, "anchor_weight": args.anchor_weight,
            "train_seconds": round(train_s, 1), "total_seconds": round(time.time() - t_start, 1),
            "device": str(dev), "before": before, "after": after}
    with open(os.path.join(args.out, TUNED_META), "w") as f:
        json.dump(meta, f, indent=2)
    print(f"wrote {args.out} (training {train_s:.0f}s, total {time.time() - t_start:.0f}s)")


if __name__ == "__main__":
    main()

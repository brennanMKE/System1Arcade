"""Tests for the fine-tuning tools: loading tuned weights (laya_batch.apply_tuned),
merging and splitting data (finetune.py) and labelling answers
(oracle_selfplay.py). They use a tiny stand-in model, so they run without
loading Laya (torch and laya must be importable):

    .venv/bin/python -m unittest discover agents
"""
import json
import os
import sys
import tempfile
import unittest

import torch

sys.path.insert(0, __file__.rsplit("/", 1)[0])
import finetune  # noqa: E402
import oracle_selfplay  # noqa: E402
from laya_batch import TUNED_META, TUNED_TENSORS, apply_tuned  # noqa: E402


class TinyModel(torch.nn.Module):
    def __init__(self):
        super().__init__()
        self.encoder = torch.nn.Linear(2, 2)
        self.scorer = torch.nn.Linear(2, 1)


class FakeAgent:
    def __init__(self):
        self.model = TinyModel()


def write_tuned(folder, tensors):
    from safetensors.torch import save_file
    save_file(tensors, os.path.join(folder, TUNED_TENSORS))
    with open(os.path.join(folder, TUNED_META), "w") as f:
        json.dump({"base_model": "test"}, f)


class ApplyTunedTest(unittest.TestCase):
    def test_replaces_only_the_saved_tensors(self):
        agent = FakeAgent()
        encoder_before = agent.model.encoder.weight.detach().clone()
        with tempfile.TemporaryDirectory() as d:
            write_tuned(d, {"scorer.weight": torch.full((1, 2), 7.0), "scorer.bias": torch.zeros(1)})
            apply_tuned(agent, d)
        self.assertTrue(torch.equal(agent.model.scorer.weight, torch.full((1, 2), 7.0)))
        self.assertTrue(torch.equal(agent.model.encoder.weight, encoder_before))
        self.assertEqual(agent.tuned["base_model"], "test")

    def test_rejects_unknown_or_misshapen_tensors(self):
        with tempfile.TemporaryDirectory() as d:
            write_tuned(d, {"nothere.weight": torch.zeros(1)})
            with self.assertRaises(ValueError):
                apply_tuned(FakeAgent(), d)
        with tempfile.TemporaryDirectory() as d:
            write_tuned(d, {"scorer.weight": torch.zeros(3, 3)})
            with self.assertRaises(ValueError):
                apply_tuned(FakeAgent(), d)


SAFE = {"type": "choice", "instructions": "What kind of place is it?",
        "criteria": {"safe": "a safe place", "deadly": "a deadly place"}}


class DataTest(unittest.TestCase):
    def test_records_merge_across_files(self):
        rec = {"game": "frogger", "group": "up", "state": "Hopping up lands in a safe place: safe ground.",
               "question": SAFE, "labels": {"safe": 3}, "count": 3, "seeds": [1]}
        with tempfile.TemporaryDirectory() as d:
            for name, seeds in (("a.jsonl", [1]), ("b.jsonl", [2])):
                with open(os.path.join(d, name), "w") as f:
                    f.write(json.dumps({**rec, "seeds": seeds}) + "\n")
            got = finetune.load_records([os.path.join(d, "*.jsonl")])
        self.assertEqual(len(got), 1)
        self.assertEqual(got[0]["labels"], {"safe": 6})
        self.assertEqual(got[0]["seeds"], {1, 2})

    def test_option_order_and_holdout_are_stable(self):
        self.assertEqual(finetune.option_keys(SAFE), ["safe", "deadly"])
        self.assertEqual(finetune.option_keys({"type": "noul", "instructions": "?"}), ["no", "yes"])
        rec = {"state": "The target is far to the left of the cannon.", "question": SAFE}
        self.assertEqual(finetune.held_out(rec, 0.2), finetune.held_out(dict(rec), 0.2))
        self.assertFalse(finetune.held_out(rec, 0.0))
        self.assertTrue(finetune.held_out(rec, 1.0))

    def test_labels_and_seeds(self):
        self.assertEqual(oracle_selfplay.seed_list("1-3,7"), [1, 2, 3, 7])
        self.assertEqual(finetune.seed_list("31-33"), {31, 32, 33})
        noul = {"type": "noul", "noul": 0.8}
        self.assertEqual(oracle_selfplay.label(noul), "yes")
        self.assertAlmostEqual(oracle_selfplay.p_of(noul, "no"), 0.2)
        choice = {"type": "choice", "probabilities": {"safe": 0.3, "deadly": 0.7}}
        self.assertEqual(oracle_selfplay.label(choice), "deadly")
        self.assertEqual(oracle_selfplay.p_of(choice, "safe"), 0.3)


if __name__ == "__main__":
    unittest.main()

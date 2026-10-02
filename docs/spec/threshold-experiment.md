# Semantic Threshold Experiment

The headline experiment: how the semantic cache similarity threshold trades savings against wrong answers. A lower threshold yields more hits and more savings, and also serves cached answers to questions that only look similar. The output is a curve and a defensible threshold choice.

Related: [0017](../adr/0017-semantic-threshold.md), [caching.md](caching.md).

## Dataset

File: `experiments/threshold/pairs.jsonl`, about 200 labeled pairs, in English.

```json
{"id": "p001", "a": "What is the capital of Australia?", "b": "Which city is Australia's capital?", "label": "same", "category": "paraphrase"}
{"id": "n001", "a": "What is the capital of Australia?", "b": "What is the capital of Austria?", "label": "different", "category": "entity_swap"}
```

### Composition

| Label | Share | Categories |
| --- | --- | --- |
| `same` | ~50% | `paraphrase`, `reorder`, `formality`, `typo`, `verbosity` |
| `different` | ~50% | `entity_swap`, `direction_swap` (10 km → miles vs 10 miles → km), `negation`, `quantity_change`, `scope_change`, `topic_neighbor` |

At least 70% of `different` pairs are hard negatives (`entity_swap`, `direction_swap`, `negation`, `quantity_change`). The others are easy negatives and serve as a floor.

### Labeling protocol

1. Candidate pairs are generated with LLM assistance from a seed list of topics. The generation prompt lives in `experiments/threshold/generate.md`.
2. **Every pair is reviewed manually** by the author. The rule: label `same` only if one correct answer to `a` is also a fully correct answer to `b`.
3. Ambiguous pairs are dropped rather than forced.
4. `experiments/threshold/LABELING.md` records the rules, the counts per category, and the number of pairs dropped.

## Procedure

`cmd/loadgen threshold`:

1. Embed both sides of every pair with each embedding model: `text-embedding-3-small`, and `text-embedding-3-large` with `dimensions=1536`. Embeddings are cached in `experiments/threshold/embeddings.<model>.jsonl` so reruns are free.
2. Compute the cosine similarity per pair.
3. Sweep `t` from 0.70 to 0.99 in steps of 0.005. For each `t` and model:
   - `hit` when `sim >= t`.
   - **Hit rate** = hits / all pairs.
   - **False hit rate (FHR)** = hits among `different` / all `different`, which is the share of incorrect answers that would be served.
   - **Recall on same** = hits among `same` / all `same`.
   - **Precision** = true hits / all hits.
   - **Estimated savings** = recall on same × mean cost per request from B2 (`results/cache.json`), expressed as USD per 1000 requests and as % of spend.
4. Choose the operating point: **the lowest `t` with FHR ≤ 1%**, which maximizes savings under a quality constraint. Also report the `t` with FHR ≤ 5%, for context.
5. Bootstrap (1000 resamples) gives 95% confidence intervals for FHR and recall at the chosen `t`.

Total API cost is about 400 embeddings per model, which is well under US$0.01.

## Outputs

`results/threshold.json`:

```json
{
  "meta": {"pairs": 200, "same": 100, "different": 100, "git_sha": "...", "generated_at": "..."},
  "models": {
    "text-embedding-3-small": {
      "curve": [{"t": 0.70, "hit_rate": 0.0, "fhr": 0.0, "recall": 0.0, "precision": 0.0, "savings_pct": 0.0}],
      "chosen": {"t": 0.0, "fhr": 0.0, "fhr_ci": [0.0, 0.0], "recall": 0.0, "recall_ci": [0.0, 0.0]},
      "worst_false_hits": [{"id": "n042", "sim": 0.0}]
    }
  }
}
```

`worst_false_hits` lists the 10 `different` pairs with the highest similarity. The Results tab shows them as concrete examples of wrong answers.

### Charts (Results tab)

1. **Main chart**: x = false hit rate (%), y = savings (%). One line per embedding model, each point is a threshold, the chosen point is annotated, and the 1% FHR line is drawn.
2. Hit rate and FHR versus threshold, one line each per model.
3. Similarity distributions of `same` versus `different` pairs (overlaid histograms), showing the overlap that makes the trade-off unavoidable.

## Decision record

After the run, [0017](../adr/0017-semantic-threshold.md) moves from Proposed to Accepted with the chosen model and threshold, the FHR and recall with their CIs, and a link to `results/threshold.json`. `cache.semantic.threshold` in config is updated to match.

## Acceptance criteria

- At least 180 pairs remain after review, with each label between 45% and 55%.
- `make experiment-threshold` is deterministic given the cached embeddings.
- The chosen threshold in config equals `results/threshold.json` → `chosen.t` for the configured embedding model (checked by a test).

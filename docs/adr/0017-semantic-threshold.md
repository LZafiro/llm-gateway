# 0017. Semantic cache similarity threshold

- Status: Proposed
- Date: 2026-10-01

## Context

A lower threshold yields more semantic hits and more savings, but also more False Hits: similar prompts whose correct answers differ. The provisional default is 0.95.

## Decision

To be filled in from the threshold experiment (see `docs/spec/threshold-experiment.md`). The experiment uses about 200 human-reviewed labelled pairs, balanced between paraphrases and hard negatives. It sweeps the threshold from 0.70 to 0.99 for `text-embedding-3-small` and `text-embedding-3-large`. The chosen threshold maximizes savings subject to a False Hit rate of at most 1%.

## Consequences

To be recorded with the experiment results and chart.

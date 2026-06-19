"""
RAGAS Evaluation for Nimbus RAG Pipeline
=========================================

RAGAS (Retrieval Augmented Generation Assessment) gives you objective,
reproducible quality scores for a RAG system. Without evaluation, you're
flying blind — you don't know if your retrieval is actually finding the
right documents, or if the LLM is hallucinating.

Three metrics we measure:

  1. Faithfulness (target > 0.70)
     "Is every claim in the answer traceable back to the retrieved context?"
     Score 0 = complete hallucination. Score 1 = fully grounded.
     HOW: Extract atomic claims from the answer, check each against context.

  2. Answer Relevancy (target > 0.70)
     "Does the answer actually address the question asked?"
     A factually correct but off-topic answer scores low.
     HOW: Ask the LLM to generate N questions from the answer, measure
          keyword overlap with the original question.

  3. Context Precision (target > 0.60)
     "Are the most relevant documents ranked highest?"
     A good retriever puts relevant docs at rank 1, not rank 19.
     HOW: For each retrieved doc, check if it's relevant to the ground
          truth. Score = average precision at each relevant rank position.

Overall RAGAS score = mean of the three = target > 0.67

Run:
    cd nimbus/ai-integration
    pip install -r requirements.txt
    OPENAI_API_KEY=sk-... python ragas_eval.py --nimbus-url http://localhost:8080
"""

import os
import json
import sys
import requests
from dataclasses import dataclass, field
from typing import List, Optional

from openai import OpenAI

NIMBUS_BASE_URL = os.getenv("NIMBUS_BASE_URL", "http://localhost:8080")
TENANT_ID = os.getenv("TEST_TENANT_ID", "00000000-0000-0000-0000-000000000001")


# ─────────────────────────────────────────────────────────────────────────────
# Evaluation dataset
# 10 questions covering Nimbus's core behaviours.
# ground_truth is the ideal answer — we use it for context_precision scoring.
# ─────────────────────────────────────────────────────────────────────────────

@dataclass
class EvalSample:
    question: str
    ground_truth: str
    answer: Optional[str] = None
    contexts: List[str] = field(default_factory=list)


TEST_DATASET: List[EvalSample] = [
    EvalSample(
        question="What notification channels does Nimbus support?",
        ground_truth="Nimbus supports three channels: email (via AWS SES), SMS (via AWS SNS), and webhook.",
    ),
    EvalSample(
        question="What happens when a notification fails to deliver?",
        ground_truth=(
            "Nimbus retries with exponential backoff: 1 minute after attempt 1, "
            "5 minutes after attempt 2, 15 minutes after attempt 3. "
            "After 3 failures the notification moves to the dead letter queue."
        ),
    ),
    EvalSample(
        question="How does Nimbus prevent sending duplicate notifications?",
        ground_truth=(
            "Nimbus uses tenant-aware idempotency keys stored in Redis. "
            "Auto-generated keys (SHA-256 content hash) expire after 5 minutes. "
            "Client-supplied Idempotency-Key header values are cached for 24 hours."
        ),
    ),
    EvalSample(
        question="What is the rate limiting policy for the API?",
        ground_truth=(
            "100 requests per minute per tenant using a Redis sliding-window rate limiter. "
            "Rate limit headers X-RateLimit-Limit, X-RateLimit-Remaining, "
            "and X-RateLimit-Reset are returned on every response."
        ),
    ),
    EvalSample(
        question="How are notifications processed after they are created?",
        ground_truth=(
            "After creation, the notification is enqueued to AWS SQS. "
            "A background worker polls SQS, picks up pending notifications in batches of 10, "
            "and dispatches them to the appropriate channel sender."
        ),
    ),
    EvalSample(
        question="What is the dead letter queue and how do I use it?",
        ground_truth=(
            "Notifications that exhaust all retry attempts are moved to the DLQ table. "
            "Operators can list DLQ items via GET /v1/dlq, retry a specific item via "
            "POST /v1/dlq/{id}/retry, or permanently discard it via POST /v1/dlq/{id}/discard."
        ),
    ),
    EvalSample(
        question="How does the circuit breaker protect downstream services?",
        ground_truth=(
            "Each channel sender (SES, SNS, webhook) is wrapped with a circuit breaker. "
            "After 5 consecutive failures the circuit opens and fast-fails for 30 seconds "
            "instead of hammering an unhealthy downstream service."
        ),
    ),
    EvalSample(
        question="What AWS services does Nimbus depend on?",
        ground_truth=(
            "Nimbus uses SQS for async queuing, SES for email, SNS for SMS, "
            "RDS PostgreSQL for persistence, ElastiCache Redis for idempotency "
            "and rate limiting, and ECS/Fargate for container orchestration."
        ),
    ),
    EvalSample(
        question="What does the gRPC NotificationService provide?",
        ground_truth=(
            "The gRPC service exposes CreateNotification (unary), GetNotification (unary), "
            "and StreamDeliveryUpdates (server-streaming). Internal callers use gRPC on "
            "port 9090 for strong typing and live delivery status streams."
        ),
    ),
    EvalSample(
        question="How does the RAG pipeline work for the /v1/ai/ask endpoint?",
        ground_truth=(
            "The pipeline embeds the user query using text-embedding-3-small, "
            "performs hybrid retrieval (pgvector cosine + full-text RRF), "
            "reranks the top-20 candidates to top-5, then generates a cited answer "
            "with the LLM. PII is masked before the LLM call and restored in the response."
        ),
    ),
]


# ─────────────────────────────────────────────────────────────────────────────
# Nimbus RAG API caller
# ─────────────────────────────────────────────────────────────────────────────

def call_rag_pipeline(question: str) -> dict:
    """Call POST /v1/ai/ask and return the JSON response."""
    try:
        resp = requests.post(
            f"{NIMBUS_BASE_URL}/v1/ai/ask",
            json={"query": question, "tenant_id": TENANT_ID},
            timeout=30,
        )
        resp.raise_for_status()
        return resp.json()
    except requests.RequestException as e:
        return {"answer": "", "citations": [], "_error": str(e)}


# ─────────────────────────────────────────────────────────────────────────────
# RAGAS metric implementations
# ─────────────────────────────────────────────────────────────────────────────

def compute_faithfulness(client: OpenAI, answer: str, contexts: List[str]) -> float:
    """
    Faithfulness = supported_claims / total_claims

    We ask the LLM to:
    1. Break the answer into atomic claims (one fact per claim).
    2. For each claim, check if it is supported by the context.

    A claim is "supported" if you can find evidence for it in the context docs.
    A claim with NO evidence = hallucination → lowers the score.

    Example:
        Answer: "Nimbus retries 3 times [1]. It uses Redis for rate limiting [2]."
        Contexts: [1] mentions retries, [2] mentions Redis.
        → both claims supported → faithfulness = 1.0
    """
    if not answer or not contexts:
        return 0.0

    context_str = "\n---\n".join(contexts[:5])  # limit to avoid token overflow
    prompt = f"""Given the context and answer below, analyze faithfulness.

Context:
{context_str}

Answer:
{answer}

Step 1: Extract every atomic factual claim from the answer as a JSON array.
Step 2: For each claim, set "supported" to true if the context contains evidence, false otherwise.

Respond with valid JSON only:
{{
  "claims": ["claim text 1", "claim text 2"],
  "supported": [true, false]
}}"""

    try:
        resp = client.chat.completions.create(
            model="gpt-4o-mini",
            messages=[{"role": "user", "content": prompt}],
            response_format={"type": "json_object"},
            temperature=0,
        )
        data = json.loads(resp.choices[0].message.content)
        supported = data.get("supported", [])
        if not supported:
            return 1.0  # no claims extracted → trivially faithful
        return sum(1 for s in supported if s) / len(supported)
    except Exception as e:
        print(f"    ⚠ faithfulness eval error: {e}")
        return 0.0


def compute_answer_relevancy(client: OpenAI, question: str, answer: str) -> float:
    """
    Answer Relevancy measures whether the answer addresses the question.

    Method (from the original RAGAS paper):
    1. Generate 3 synthetic questions that the answer could be responding to.
    2. Compute keyword overlap between each synthetic question and the real question.
    3. Score = mean overlap across the 3 synthetic questions.

    Intuition: if the answer is truly relevant to the question, you should be
    able to reconstruct roughly the same question from the answer alone.

    A high relevancy score means: "Given this answer, a reasonable person would
    guess the question was: '{original question}'."
    """
    if not answer:
        return 0.0

    prompt = f"""Given the following answer, generate 3 different questions that this answer is responding to.

Answer: {answer}

Respond with valid JSON only:
{{"questions": ["question 1", "question 2", "question 3"]}}"""

    try:
        resp = client.chat.completions.create(
            model="gpt-4o-mini",
            messages=[{"role": "user", "content": prompt}],
            response_format={"type": "json_object"},
            temperature=0.3,
        )
        data = json.loads(resp.choices[0].message.content)
        synthetic_qs = data.get("questions", [])

        original_words = set(question.lower().split())
        scores = []
        for sq in synthetic_qs[:3]:
            if isinstance(sq, str):
                sq_words = set(sq.lower().split())
                overlap = len(original_words & sq_words) / max(len(original_words | sq_words), 1)
                scores.append(overlap)

        return sum(scores) / len(scores) if scores else 0.5
    except Exception as e:
        print(f"    ⚠ relevancy eval error: {e}")
        return 0.5


def compute_context_precision(contexts: List[str], ground_truth: str) -> float:
    """
    Context Precision = Average Precision at K

    For each retrieved document, we check if it's "relevant" (shares significant
    keywords with the ground truth answer). Then we compute precision at each
    rank where a relevant document appears.

    Example with 4 retrieved docs [relevant, irrelevant, relevant, irrelevant]:
        At rank 1: 1 relevant in top-1 → precision@1 = 1/1 = 1.0
        At rank 3: 2 relevant in top-3 → precision@3 = 2/3 = 0.67
        AP = (1.0 + 0.67) / 2 = 0.83  ← good: relevant docs are at top

    Example with [irrelevant, irrelevant, relevant, relevant]:
        At rank 3: 1 relevant in top-3 → precision@3 = 1/3 = 0.33
        At rank 4: 2 relevant in top-4 → precision@4 = 2/4 = 0.50
        AP = (0.33 + 0.50) / 2 = 0.42  ← poor: relevant docs are buried
    """
    if not contexts:
        return 0.0

    gt_words = set(ground_truth.lower().split())

    precision_at_k = []
    relevant_count = 0

    for k, ctx in enumerate(contexts, 1):
        ctx_words = set(ctx.lower().split())
        overlap = len(gt_words & ctx_words) / max(len(gt_words), 1)
        is_relevant = overlap > 0.08  # ≥8% keyword overlap = relevant

        if is_relevant:
            relevant_count += 1
            precision_at_k.append(relevant_count / k)

    if not precision_at_k:
        return 0.0
    return sum(precision_at_k) / len(precision_at_k)


# ─────────────────────────────────────────────────────────────────────────────
# Evaluation runner
# ─────────────────────────────────────────────────────────────────────────────

def run_evaluation(dataset: List[EvalSample] = TEST_DATASET) -> dict:
    api_key = os.getenv("OPENAI_API_KEY")
    if not api_key:
        raise SystemExit("❌ Missing OPENAI_API_KEY. Run: export OPENAI_API_KEY=sk-...")

    client = OpenAI(api_key=api_key)

    print(f"╔══════════════════════════════════════════════════════╗")
    print(f"║       RAGAS Evaluation — Nimbus RAG Pipeline         ║")
    print(f"╚══════════════════════════════════════════════════════╝")
    print(f"  Nimbus URL : {NIMBUS_BASE_URL}")
    print(f"  Tenant ID  : {TENANT_ID}")
    print(f"  Samples    : {len(dataset)}\n")

    results = []
    pipeline_down = False

    for i, sample in enumerate(dataset):
        print(f"[{i+1:02d}/{len(dataset)}] {sample.question[:65]}")

        # ── Call RAG pipeline ────────────────────────────────────────────────
        rag_resp = call_rag_pipeline(sample.question)

        if "_error" in rag_resp or not rag_resp.get("answer"):
            if not pipeline_down:
                print(f"\n  ⛔  Cannot reach Nimbus at {NIMBUS_BASE_URL}")
                print(f"      Start with: cd ~/workspace/nimbus && go run ./cmd/gateway")
                print(f"      Then re-run this script.\n")
                pipeline_down = True
            results.append({
                "question": sample.question,
                "faithfulness": 0.0,
                "answer_relevancy": 0.0,
                "context_precision": 0.0,
                "error": rag_resp.get("_error", "empty answer"),
            })
            continue

        sample.answer = rag_resp["answer"]
        sample.contexts = [c.get("content", "") for c in rag_resp.get("citations", [])]

        # ── Compute RAGAS metrics ─────────────────────────────────────────────
        faithfulness   = compute_faithfulness(client, sample.answer, sample.contexts)
        relevancy      = compute_answer_relevancy(client, sample.question, sample.answer)
        ctx_precision  = compute_context_precision(sample.contexts, sample.ground_truth)

        print(f"       Faithfulness:      {faithfulness:.3f}")
        print(f"       Answer Relevancy:  {relevancy:.3f}")
        print(f"       Context Precision: {ctx_precision:.3f}")

        results.append({
            "question":          sample.question,
            "answer_snippet":    sample.answer[:120] + "..." if len(sample.answer) > 120 else sample.answer,
            "faithfulness":      round(faithfulness, 3),
            "answer_relevancy":  round(relevancy, 3),
            "context_precision": round(ctx_precision, 3),
            "num_citations":     len(sample.contexts),
        })

    # ── Aggregate ─────────────────────────────────────────────────────────────
    avg_faith  = sum(r["faithfulness"]      for r in results) / len(results)
    avg_relev  = sum(r["answer_relevancy"]  for r in results) / len(results)
    avg_prec   = sum(r["context_precision"] for r in results) / len(results)
    ragas_score = (avg_faith + avg_relev + avg_prec) / 3

    summary = {
        "total_samples": len(results),
        "metrics": {
            "faithfulness":      round(avg_faith, 3),
            "answer_relevancy":  round(avg_relev, 3),
            "context_precision": round(avg_prec, 3),
            "ragas_score":       round(ragas_score, 3),
        },
        "targets": {
            "faithfulness":      0.70,
            "answer_relevancy":  0.70,
            "context_precision": 0.60,
            "ragas_score":       0.67,
        },
        "per_sample": results,
    }

    def status(score, target):
        return "✅" if score >= target else "❌"

    print(f"\n{'═'*55}")
    print(f"  RAGAS EVALUATION SUMMARY")
    print(f"{'═'*55}")
    print(f"  Faithfulness:      {avg_faith:.3f}  (target ≥0.70) {status(avg_faith, 0.70)}")
    print(f"  Answer Relevancy:  {avg_relev:.3f}  (target ≥0.70) {status(avg_relev, 0.70)}")
    print(f"  Context Precision: {avg_prec:.3f}  (target ≥0.60) {status(avg_prec, 0.60)}")
    print(f"  ─────────────────────────────────────────────────")
    print(f"  RAGAS Score:       {ragas_score:.3f}  (target ≥0.67) {status(ragas_score, 0.67)}")
    print(f"{'═'*55}\n")

    # Save results for CI upload / historical tracking
    output_path = os.path.join(os.path.dirname(__file__), "ragas_results.json")
    with open(output_path, "w") as f:
        json.dump(summary, f, indent=2)
    print(f"  Results saved → {output_path}")

    return summary


# ─────────────────────────────────────────────────────────────────────────────
# CLI
# ─────────────────────────────────────────────────────────────────────────────

def main():
    import argparse

    parser = argparse.ArgumentParser(description="RAGAS evaluation for Nimbus RAG pipeline")
    parser.add_argument("--nimbus-url", default="http://localhost:8080", help="Nimbus base URL")
    parser.add_argument("--tenant-id",  default=TENANT_ID,             help="Tenant UUID for test queries")
    args = parser.parse_args()

    global NIMBUS_BASE_URL, TENANT_ID
    NIMBUS_BASE_URL = args.nimbus_url
    TENANT_ID = args.tenant_id

    summary = run_evaluation()
    ragas_score = summary["metrics"]["ragas_score"]

    # Exit with non-zero if score is below threshold — useful in CI
    if ragas_score < 0.50:
        print(f"⚠  RAGAS score {ragas_score:.3f} is below minimum threshold 0.50")
        sys.exit(1)


if __name__ == "__main__":
    main()

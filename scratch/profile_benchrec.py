import csv, re, sys, random, collections

csv.field_size_limit(10**7)
PATH = "data/benchrec/BenchRec_cash_v1.0_train.csv"
TOKEN = re.compile(r"[A-Za-z0-9#]{4,}")

KEEP = ["matchId","matchRule","matchedBy",
        "A_id","A_allocation","A_debitOrCredit","A_amount","A_valueDate","A_currencyCode","A_account","A_transactionReferences","A_transactionAttributes",
        "B_id","B_debitOrCredit","B_amount","B_valueDate","B_currencyCode","B_account","B_transactionReferences","B_transactionAttributes"]

groups = collections.defaultdict(list)
total_rows = 0
with open(PATH, newline="") as f:
    for row in csv.DictReader(f):
        total_rows += 1
        groups[row["matchId"]].append({k: row[k] for k in KEEP})

print(f"rows={total_rows}  distinct matchIds={len(groups)}\n")

# ---------- Q1: subset shape ----------
shape = collections.Counter()
pairs = []
for mid, rows in groups.items():
    A = [r for r in rows if r["A_id"]]
    B = [r for r in rows if r["B_id"]]
    both = [r for r in rows if r["A_id"] and r["B_id"]]
    if both:
        shape["row_has_both_legs"] += 1
        continue
    shape[f"{len(A)}:{len(B)}"] += 1
    if len(A) == 1 and len(B) == 1:
        pairs.append((mid, A[0], B[0], rows))

print("=== Q1: group cardinality (A_rows:B_rows) ===")
for k, v in shape.most_common(12):
    print(f"  {k:>22}  {v:6d}  ({100*v/len(groups):.1f}%)")
print(f"  1:1 both-sided total = {len(pairs)}\n")

def cents(s):
    if not s: return None
    m = re.fullmatch(r"(-?)(\d+)\.(\d{2})", s.strip())
    if not m: return "BAD"
    sign = -1 if m.group(1) else 1
    return sign * (int(m.group(2)) * 100 + int(m.group(3)))

# ---------- Q4: amount + direction ----------
print("=== Q4: amount parsing and direction ===")
bad = neg_a = neg_b = 0
dirtab = collections.Counter()
signtab = collections.Counter()
absdiff = []
for mid, a, b, _ in pairs:
    ca, cb = cents(a["A_amount"]), cents(b["B_amount"])
    if ca in (None, "BAD") or cb in (None, "BAD"):
        bad += 1; continue
    if ca < 0: neg_a += 1
    if cb < 0: neg_b += 1
    dirtab[(a["A_debitOrCredit"], b["B_debitOrCredit"])] += 1
    signtab[(a["A_debitOrCredit"], "neg" if ca < 0 else "pos")] += 1
    absdiff.append(abs(abs(ca) - abs(cb)))
print(f"  rows failing strict 2dp decimal parse: {bad}")
print(f"  negative A_amount: {neg_a} ({100*neg_a/len(pairs):.1f}%)   negative B_amount: {neg_b} ({100*neg_b/len(pairs):.1f}%)")
print("  (A_debitOrCredit, B_debitOrCredit) for true pairs:")
for k, v in dirtab.most_common():
    print(f"     {str(k):>20}  {v:6d}  ({100*v/len(absdiff):.1f}%)")
print("  (A_debitOrCredit, sign of A_amount):")
for k, v in signtab.most_common():
    print(f"     {str(k):>20}  {v:6d}")
absdiff.sort()
exact = sum(1 for d in absdiff if d == 0)
within1 = sum(1 for d in absdiff if d <= 1)
print(f"  |amount| agrees exactly: {exact} ({100*exact/len(absdiff):.1f}%)")
print(f"  |amount| within 1 cent : {within1} ({100*within1/len(absdiff):.1f}%)")
nz = [d for d in absdiff if d > 0]
if nz:
    print(f"  nonzero gap: n={len(nz)} median=${nz[len(nz)//2]/100:,.2f} p95=${nz[int(.95*len(nz))]/100:,.2f} max=${nz[-1]/100:,.2f}")
maxamt = max(abs(cents(a['A_amount']) or 0) for _, a, _, _ in pairs if cents(a['A_amount']) not in (None,'BAD'))
print(f"  max |A_amount| in cents: {maxamt}  (int64 max 9.22e18)\n")

# ---------- Q2/Q3: text fields ----------
print("=== Q2/Q3: text field population (1:1 subset) ===")
FIELDS = ["A_transactionReferences","A_transactionAttributes","A_allocation",
          "B_transactionReferences","B_transactionAttributes"]
empty = collections.Counter()
toklen = collections.Counter()
for mid, a, b, _ in pairs:
    src = {**a, **b}
    for fld in FIELDS:
        v = src.get(fld, "").strip()
        if not v: empty[fld] += 1
        else: toklen[fld] += len(set(TOKEN.findall(v.upper())))
n = len(pairs)
for fld in FIELDS:
    e = empty[fld]
    avg = toklen[fld]/max(n-e,1)
    print(f"  {fld:<28} empty {e:6d} ({100*e/n:5.1f}%)   avg distinct 4+char tokens {avg:5.1f}")

print("\n=== Q3: pairs lost if CounterpartyRef must be non-empty on BOTH legs ===")
def toks(r, flds):
    s = " ".join(r.get(f, "") for f in flds)
    return set(TOKEN.findall(s.upper()))
OPTIONS = {
    "references only":            (["A_transactionReferences"], ["B_transactionReferences"]),
    "attributes only":            (["A_transactionAttributes"], ["B_transactionAttributes"]),
    "references + attributes":    (["A_transactionReferences","A_transactionAttributes"], ["B_transactionReferences","B_transactionAttributes"]),
    "refs + attrs + allocation":  (["A_transactionReferences","A_transactionAttributes","A_allocation"], ["B_transactionReferences","B_transactionAttributes"]),
}
for name, (af, bf) in OPTIONS.items():
    lost = sum(1 for _, a, b, _ in pairs if not toks(a, af) or not toks(b, bf))
    print(f"  {name:<28} drops {lost:6d} pairs ({100*lost/n:5.2f}%)  -> usable {n-lost}")

print("\n=== Q2: where does shared signal live? (% of true pairs with >=1 shared 4+char token) ===")
PAIRINGS = [
    ("A_refs  vs B_refs",   ["A_transactionReferences"], ["B_transactionReferences"]),
    ("A_refs  vs B_attrs",  ["A_transactionReferences"], ["B_transactionAttributes"]),
    ("A_attrs vs B_refs",   ["A_transactionAttributes"], ["B_transactionReferences"]),
    ("A_attrs vs B_attrs",  ["A_transactionAttributes"], ["B_transactionAttributes"]),
    ("A_alloc vs B_attrs",  ["A_allocation"],            ["B_transactionAttributes"]),
    ("A_all   vs B_all",    ["A_transactionReferences","A_transactionAttributes","A_allocation"],
                            ["B_transactionReferences","B_transactionAttributes"]),
]
for name, af, bf in PAIRINGS:
    hit = 0; shared_counts = []
    for _, a, b, _ in pairs:
        s = toks(a, af) & toks(b, bf)
        if s: hit += 1
        shared_counts.append(len(s))
    shared_counts.sort()
    print(f"  {name:<22} >=1 shared: {100*hit/n:5.1f}%   median shared tokens {shared_counts[len(shared_counts)//2]}")

# ---------- Q5: id uniqueness ----------
print("\n=== Q5: identifier uniqueness ===")
aid = collections.Counter(); bid = collections.Counter()
for mid, rows in groups.items():
    for r in rows:
        if r["A_id"]: aid[r["A_id"]] += 1
        if r["B_id"]: bid[r["B_id"]] += 1
print(f"  distinct A_id {len(aid)}  duplicated {sum(1 for v in aid.values() if v>1)}")
print(f"  distinct B_id {len(bid)}  duplicated {sum(1 for v in bid.values() if v>1)}")
print(f"  A_id/B_id namespace overlap: {len(set(aid) & set(bid))}")

# ---------- Q6: retrieval ceiling ----------
print("\n=== Q6: candidate-retrieval ceiling (engine: same valueDate +/-120s, amount +/-1 cent) ===")
same_day = 0; retrievable = 0; daydelta = collections.Counter()
import datetime
def d(s):
    return datetime.date.fromisoformat(s) if s else None
for _, a, b, _ in pairs:
    ca, cb = cents(a["A_amount"]), cents(b["B_amount"])
    if ca in (None,"BAD") or cb in (None,"BAD"): continue
    da, db = d(a["A_valueDate"]), d(b["B_valueDate"])
    delta = abs((da - db).days) if da and db else None
    daydelta[delta] += 1
    if delta == 0:
        same_day += 1
        if abs(abs(ca) - abs(cb)) <= 1: retrievable += 1
print("  |valueDate delta| in days:")
for k in sorted([x for x in daydelta if x is not None])[:8]:
    print(f"     {k:>3} day  {daydelta[k]:6d}  ({100*daydelta[k]/n:5.1f}%)")
print(f"  same valueDate                        : {same_day} ({100*same_day/n:.1f}%)")
print(f"  same valueDate AND within 1 cent      : {retrievable} ({100*retrievable/n:.1f}%)  <-- HARD RECALL CEILING")

# ---------- Q6b: collisions and token discrimination ----------
print("\n=== Q6b: among retrievable pairs, do tokens break the tie? ===")
bucket = collections.defaultdict(list)
for _, a, b, _ in pairs:
    cb = cents(b["B_amount"])
    if cb in (None,"BAD"): continue
    bucket[(b["B_valueDate"], abs(cb))].append(b)

random.seed(7)
sample = [p for p in pairs]
random.shuffle(sample)
sample = sample[:8000]
alone = 0; contested = 0; win = 0; tie = 0; lose = 0
for _, a, b, _ in sample:
    ca, cb = cents(a["A_amount"]), cents(b["B_amount"])
    if ca in (None,"BAD") or cb in (None,"BAD"): continue
    if a["A_valueDate"] != b["B_valueDate"] or abs(abs(ca)-abs(cb)) > 1: continue
    cands = []
    for delta in (-1, 0, 1):
        cands.extend(bucket.get((a["A_valueDate"], abs(ca)+delta), []))
    cands = {c["B_id"]: c for c in cands}.values()
    if len(cands) <= 1:
        alone += 1; continue
    contested += 1
    ta = toks(a, ["A_transactionReferences","A_transactionAttributes","A_allocation"])
    scores = [(len(ta & toks(c, ["B_transactionReferences","B_transactionAttributes"])), c["B_id"]) for c in cands]
    best = max(s for s, _ in scores)
    mine = next(s for s, i in scores if i == b["B_id"])
    if mine < best: lose += 1
    elif sum(1 for s, _ in scores if s == best) > 1: tie += 1
    else: win += 1
tot = alone + contested
print(f"  sampled retrievable pairs: {tot}")
print(f"  uncontested (only true candidate in bucket): {alone} ({100*alone/max(tot,1):.1f}%)")
print(f"  contested  (>=2 candidates same day+amount): {contested} ({100*contested/max(tot,1):.1f}%)")
if contested:
    print(f"     token overlap picks TRUE outright : {win} ({100*win/contested:.1f}%)")
    print(f"     ties at the top                   : {tie} ({100*tie/contested:.1f}%)")
    print(f"     true leg loses to a decoy         : {lose} ({100*lose/contested:.1f}%)")

# ---------- token frequency ----------
print("\n=== token document frequency (top 15) — IDF candidates ===")
df = collections.Counter()
for _, a, b, _ in pairs[:20000]:
    for t in toks(a, ["A_transactionReferences","A_transactionAttributes"]) | toks(b, ["B_transactionReferences","B_transactionAttributes"]):
        df[t] += 1
for t, c in df.most_common(15):
    print(f"  {t[:40]:<42} {c}")

# ---------- strata ----------
print("\n=== strata (1:1 subset) ===")
rule = collections.Counter(); by = collections.Counter()
for mid, a, b, rows in pairs:
    r = next((x["matchRule"] for x in rows if x["matchRule"]), "")
    rule[r or "(blank)"] += 1
    by["MANUAL" if any(x["matchedBy"] == "MANUAL" for x in rows) else "AUTO"] += 1
for k, v in rule.most_common():
    print(f"  {k:<12} {v:6d} ({100*v/n:.1f}%)")
print(f"  MANUAL {by['MANUAL']} ({100*by['MANUAL']/n:.1f}%)   AUTO {by['AUTO']}")

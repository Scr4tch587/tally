import csv, re, collections, random, datetime

csv.field_size_limit(10**7)
PATH = "data/benchrec/BenchRec_cash_v1.0_train.csv"
RUN = re.compile(r"[A-Za-z0-9#]+")

groups = collections.defaultdict(list)
with open(PATH, newline="") as f:
    for row in csv.DictReader(f):
        groups[row["matchId"]].append(row)

pairs = []
for mid, rows in groups.items():
    A = [r for r in rows if r["A_id"] and not r["B_id"]]
    B = [r for r in rows if r["B_id"] and not r["A_id"]]
    if len(A) == 1 and len(B) == 1 and len(rows) == 2:
        pairs.append((mid, A[0], B[0]))
n = len(pairs)

AF = ["A_transactionReferences", "A_transactionAttributes"]
BF = ["B_transactionReferences", "B_transactionAttributes"]

def grams(text, k):
    out = set()
    for run in RUN.findall(text.upper()):
        for i in range(len(run) - k + 1):
            out.add(run[i:i+k])
    return out

def gA(a, k): return grams(" ".join(a[f] for f in AF), k)
def gB(b, k): return grams(" ".join(b[f] for f in BF), k)

print("=== recall of the text primitive: % of true 1:1 pairs sharing >=1 unit ===")
for k in (4, 5, 6, 8, 10, 12):
    hit = sum(1 for _, a, b in pairs if gA(a, k) & gB(b, k))
    print(f"  char {k:>2}-gram   {100*hit/n:5.1f}%")
hit = sum(1 for _, a, b in pairs if set(re.findall(r"[A-Za-z0-9#]{4,}", " ".join(a[f] for f in AF).upper()))
                                   & set(re.findall(r"[A-Za-z0-9#]{4,}", " ".join(b[f] for f in BF).upper())))
print(f"  whole token 4+ {100*hit/n:5.1f}%   (for comparison)")

def cents(s):
    m = re.fullmatch(r"(-?)(\d+)\.(\d{2})", (s or "").strip())
    if not m: return None
    return (-1 if m.group(1) else 1) * (int(m.group(2)) * 100 + int(m.group(3)))

# candidate buckets exactly as the engine retrieves: same valueDate, amount +/-1 cent
bucket = collections.defaultdict(list)
for _, a, b in pairs:
    cb = cents(b["B_amount"])
    if cb is None: continue
    bucket[(b["B_valueDate"], abs(cb))].append(b)

print("\n=== discrimination: among CONTESTED buckets, does the primitive pick the true leg? ===")
print("    (contested = >=2 B legs share the same valueDate and amount +/-1c)")
random.seed(11)
sample = pairs[:]
random.shuffle(sample)
sample = sample[:12000]

for k in (6, 8, 10):
    alone = win = tie = lose = blank = 0
    for _, a, b in sample:
        ca, cb = cents(a["A_amount"]), cents(b["B_amount"])
        if ca is None or cb is None: continue
        if a["A_valueDate"] != b["B_valueDate"] or abs(abs(ca) - abs(cb)) > 1: continue
        cands = {}
        for d in (-1, 0, 1):
            for c in bucket.get((a["A_valueDate"], abs(ca) + d), []):
                cands[c["B_id"]] = c
        if len(cands) <= 1:
            alone += 1; continue
        ta = gA(a, k)
        scored = [(len(ta & gB(c, k)), bid) for bid, c in cands.items()]
        best = max(s for s, _ in scored)
        mine = next(s for s, bid in scored if bid == b["B_id"])
        if best == 0: blank += 1
        elif mine < best: lose += 1
        elif sum(1 for s, _ in scored if s == best) > 1: tie += 1
        else: win += 1
    contested = win + tie + lose + blank
    print(f"\n  char {k}-gram, overlap-count scoring   (uncontested {alone}, contested {contested})")
    print(f"     picks TRUE outright        {win:5d}  ({100*win/max(contested,1):5.1f}%)")
    print(f"     tie at top                 {tie:5d}  ({100*tie/max(contested,1):5.1f}%)")
    print(f"     no candidate has any overlap{blank:4d}  ({100*blank/max(contested,1):5.1f}%)")
    print(f"     TRUE loses to a decoy      {lose:5d}  ({100*lose/max(contested,1):5.1f}%)")

print("\n=== how contested is the space overall? ===")
sizes = collections.Counter(len(v) for v in bucket.values())
tot = sum(sizes.values())
multi = sum(v for k_, v in sizes.items() if k_ > 1)
print(f"  distinct (valueDate, |amount|) buckets on B side: {tot}")
print(f"  buckets holding >1 B leg: {multi} ({100*multi/tot:.1f}%)")
for sz in sorted(sizes)[:6]:
    print(f"     size {sz:>2}: {sizes[sz]} buckets")
big = max(sizes)
print(f"     largest bucket: {big} B legs")

print("\n=== strata: text-primitive recall by matchRule (char 8-gram) ===")
by_rule = collections.defaultdict(lambda: [0, 0])
for mid, a, b in pairs:
    rule = a["matchRule"] or b["matchRule"] or "(blank)"
    by_rule[rule][1] += 1
    if gA(a, 8) & gB(b, 8): by_rule[rule][0] += 1
for rule, (h, t) in sorted(by_rule.items(), key=lambda x: -x[1][1]):
    print(f"  {rule:<12} {h:6d}/{t:<6d}  {100*h/t:5.1f}%")

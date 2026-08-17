import csv, re, collections

csv.field_size_limit(10**7)
PATH = "data/benchrec/BenchRec_cash_v1.0_train.csv"

groups = collections.defaultdict(list)
with open(PATH, newline="") as f:
    for row in csv.DictReader(f):
        groups[row["matchId"]].append(row)

def cents(s):
    m = re.fullmatch(r"(-?)(\d+)\.(\d{2})", (s or "").strip())
    if not m: return None
    return (-1 if m.group(1) else 1) * (int(m.group(2)) * 100 + int(m.group(3)))

pairs, all_B, all_A = [], [], []
for mid, rows in groups.items():
    A = [r for r in rows if r["A_id"] and not r["B_id"]]
    B = [r for r in rows if r["B_id"] and not r["A_id"]]
    for r in rows:
        if r["B_id"] and not r["A_id"]: all_B.append(r)
        if r["A_id"] and not r["B_id"]: all_A.append(r)
    if len(A) == 1 and len(B) == 1 and len(rows) == 2:
        pairs.append((mid, A[0], B[0]))

print(f"1:1 pairs {len(pairs)}   all A legs in file {len(all_A)}   all B legs in file {len(all_B)}\n")

def contest_report(label, b_universe):
    bucket = collections.defaultdict(list)
    for b in b_universe:
        cb = cents(b["B_amount"])
        if cb is None: continue
        bucket[(b["B_valueDate"], abs(cb))].append(b["B_id"])
    alone = contested = 0
    sizes = []
    for _, a, b in pairs:
        ca = cents(a["A_amount"])
        if ca is None: continue
        cands = set()
        for d in (-1, 0, 1):
            cands.update(bucket.get((a["A_valueDate"], abs(ca) + d), []))
        sizes.append(len(cands))
        if len(cands) <= 1: alone += 1
        else: contested += 1
    tot = alone + contested
    sizes.sort()
    print(f"  {label}")
    print(f"     retrievable true pairs      {tot}")
    print(f"     uncontested (1 candidate)   {alone} ({100*alone/tot:.1f}%)")
    print(f"     contested  (>1 candidate)   {contested} ({100*contested/tot:.1f}%)")
    print(f"     candidates per A leg: median {sizes[len(sizes)//2]}  p95 {sizes[int(.95*len(sizes))]}  max {sizes[-1]}")

print("=== how contested is the bucket, by what you choose to replay ===")
contest_report("REPLAY = 1:1 subset only (guide's Part 1/2 scope)", [b for _, _, b in pairs])
print()
contest_report("REPLAY = every B leg in the file (1:1 + N:M + one-sided)", all_B)

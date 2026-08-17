import csv, re, collections, random

csv.field_size_limit(10**7)
PATH = "data/benchrec/BenchRec_cash_v1.0_train.csv"
TOKEN = re.compile(r"[A-Za-z0-9#]{4,}")

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
print(f"1:1 both-sided pairs (strict, 2 rows) = {len(pairs)}\n")

AF = ["A_transactionReferences", "A_transactionAttributes", "A_allocation"]
BF = ["B_transactionReferences", "B_transactionAttributes"]

print("=== field population, read from the CORRECT row ===")
n = len(pairs)
for fld in AF:
    e = sum(1 for _, a, _ in pairs if not a[fld].strip())
    print(f"  {fld:<28} empty {e:6d} ({100*e/n:5.1f}%)")
for fld in BF:
    e = sum(1 for _, _, b in pairs if not b[fld].strip())
    print(f"  {fld:<28} empty {e:6d} ({100*e/n:5.1f}%)")

print("\n=== 5 random 1:1 pairs, raw text fields ===")
random.seed(3)
for mid, a, b in random.sample(pairs, 5):
    print(f"\n--- matchId {mid}  rule={a['matchRule'] or b['matchRule']!r} ---")
    for fld in AF:
        print(f"  {fld:<26} = {a[fld]!r}")
    for fld in BF:
        print(f"  {fld:<26} = {b[fld]!r}")
    ta = set(TOKEN.findall(" ".join(a[f] for f in AF).upper()))
    tb = set(TOKEN.findall(" ".join(b[f] for f in BF).upper()))
    print(f"  shared 4+char tokens        = {sorted(ta & tb)}")

print("\n=== tokenization sensitivity: % of true pairs with >=1 shared token ===")
def variants(s):
    u = s.upper()
    return {
        "alnum 4+":      set(re.findall(r"[A-Za-z0-9#]{4,}", u)),
        "alnum 3+":      set(re.findall(r"[A-Za-z0-9#]{3,}", u)),
        "digits 4+":     set(re.findall(r"\d{4,}", u)),
        "letters 4+":    set(re.findall(r"[A-Z]{4,}", u)),
        "whitespace":    set(w for w in re.split(r"[\s_]+", u) if len(w) >= 4),
    }
keys = ["alnum 4+","alnum 3+","digits 4+","letters 4+","whitespace"]
for k in keys:
    hit = 0
    for _, a, b in pairs:
        ta = variants(" ".join(a[f] for f in AF))[k]
        tb = variants(" ".join(b[f] for f in BF))[k]
        if ta & tb: hit += 1
    print(f"  {k:<14} {100*hit/n:5.1f}%")

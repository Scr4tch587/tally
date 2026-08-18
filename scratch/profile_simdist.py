import csv, re, collections, random
csv.field_size_limit(10**7)
RUN = re.compile(r"[A-Za-z0-9#]+")
g = collections.defaultdict(list)
for row in csv.DictReader(open('data/benchrec/BenchRec_cash_v1.0_train.csv', newline='')):
    g[row['matchId']].append(row)

def cents(s):
    m = re.fullmatch(r'(-?)(\d+)\.(\d{2})', (s or '').strip())
    return None if not m else int(m.group(2))*100+int(m.group(3))

pairs = []
for mid, rows in g.items():
    A = [r for r in rows if r['A_id'] and not r['B_id']]
    B = [r for r in rows if r['B_id'] and not r['A_id']]
    if len(A) == 1 and len(B) == 1 and len(rows) == 2:
        pairs.append((mid, A[0], B[0]))

def grams(text, k=6):
    out = set()
    for run in RUN.findall(text.upper()):
        for i in range(len(run)-k+1):
            out.add(run[i:i+k])
    return out

def ctext(r, side):
    return " ".join(r[side+f] for f in ('_transactionReferences', '_transactionAttributes'))

def overlap_coef(a, b):
    if not a or not b: return 0.0
    return len(a & b) / min(len(a), len(b))

def jaccard(a, b):
    if not a or not b: return 0.0
    return len(a & b) / len(a | b)

bucket = collections.defaultdict(list)
for _, a, b in pairs:
    cb = cents(b['B_amount'])
    if cb is not None: bucket[(b['B_valueDate'], cb)].append(b)

random.seed(5)
sample = pairs[:]; random.shuffle(sample); sample = sample[:12000]

true_oc, true_jc, decoy_oc = [], [], []
for _, a, b in sample:
    ca = cents(a['A_amount'])
    if ca is None: continue
    ga = grams(ctext(a, 'A'))
    true_oc.append(overlap_coef(ga, grams(ctext(b, 'B'))))
    true_jc.append(jaccard(ga, grams(ctext(b, 'B'))))
    for d in (-1, 0, 1):
        for c in bucket.get((a['A_valueDate'], ca+d), []):
            if c['B_id'] != b['B_id']:
                decoy_oc.append(overlap_coef(ga, grams(ctext(c, 'B'))))

def pct(v, p):
    v = sorted(v); return v[min(int(p/100*len(v)), len(v)-1)]

print(f"TRUE pairs n={len(true_oc)}   real decoys n={len(decoy_oc)}\n")
print("overlap coefficient  |A^B| / min(|A|,|B|)   on 6-grams")
print(f"  true  p5={pct(true_oc,5):.3f} p10={pct(true_oc,10):.3f} p25={pct(true_oc,25):.3f} median={pct(true_oc,50):.3f} p75={pct(true_oc,75):.3f}")
print(f"  decoy median={pct(decoy_oc,50):.3f} p90={pct(decoy_oc,90):.3f} p99={pct(decoy_oc,99):.3f} max={max(decoy_oc) if decoy_oc else 0:.3f}")
print(f"\njaccard on 6-grams (for contrast)")
print(f"  true  p25={pct(true_jc,25):.3f} median={pct(true_jc,50):.3f} p75={pct(true_jc,75):.3f}")
print("\nseparation: share of TRUE >= t  vs  share of DECOY >= t")
for t in (0.05, 0.10, 0.15, 0.20, 0.30, 0.40, 0.50, 0.60):
    tr = sum(1 for v in true_oc if v >= t)/len(true_oc)
    de = sum(1 for v in decoy_oc if v >= t)/len(decoy_oc) if decoy_oc else 0
    print(f"  t={t:.2f}   true {100*tr:5.1f}%   decoy {100*de:5.1f}%")

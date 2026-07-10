#!/usr/bin/env python3
"""
Scrape Google Images (public HTML, no API) for each product in products.tsv.
Query = Brand + Product_Name + Size_or_Weight
Fetch first 3 image URLs (.jpg/.jpeg/.png/.webp).
Wait 1s between queries (polite). Log progress. Output is true TSV (tab-separated).

Usage:
  python3 image_scrape_from_products_fixed.py --input products.tsv --output image_results.tsv
"""

import argparse
import sys
import time
import random
import urllib.parse
import urllib.request
import re

# ------------- Config -------------
USER_AGENT = (
    "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) "
    "AppleWebKit/537.36 (KHTML, like Gecko) "
    "Chrome/119.0 Safari/537.36"
)
SEARCH_URL_TPL = "https://www.google.com/search?tbm=isch&q={q}"
ALLOWED_EXTS = (".jpg", ".jpeg", ".png", ".webp")
REQUEST_TIMEOUT = 12
RETRY_ATTEMPTS = 2
DELAY_BETWEEN_QUERIES = 1.0
BLACKLIST_EXACT = {
    "https://ssl.gstatic.com/gb/images/bar/al-icon.png",
}
# ----------------------------------

HEADERS = {"User-Agent": USER_AGENT}

def fetch_html(url: str) -> str:
    """Fetch HTML with light retry logic."""
    req = urllib.request.Request(url, headers=HEADERS)
    for attempt in range(RETRY_ATTEMPTS + 1):
        try:
            with urllib.request.urlopen(req, timeout=REQUEST_TIMEOUT) as resp:
                if getattr(resp, "status", 200) == 200:
                    return resp.read().decode("utf-8", errors="ignore")
        except Exception as e:
            if attempt < RETRY_ATTEMPTS:
                time.sleep(0.5 * (attempt + 1))
            else:
                sys.stderr.write(f"Fetch error: {e}\n")
    return ""

def is_blacklisted(url: str) -> bool:
    u = url.lower()
    if url in BLACKLIST_EXACT:
        return True
    if u.startswith("https://ssl.gstatic.com/") and "/gb/images/bar/" in u:
        return True
    return False

def extract_image_links(html: str, want: int = 3):
    pattern = r"https?://[^\"']+\.(?:jpg|jpeg|png|webp)"
    matches = re.findall(pattern, html, re.IGNORECASE)
    results = []
    for m in matches:
        base = m.split("?", 1)[0].split("#", 1)[0].lower()
        if not base.endswith(ALLOWED_EXTS):
            continue
        if is_blacklisted(m):
            continue
        results.append(m)
        if len(results) >= want:
            break
    return results

def parse_tsv(path: str):
    """Read TSV into list of dicts."""
    with open(path, "r", encoding="utf-8") as f:
        lines = [ln.strip("\n") for ln in f if ln.strip()]
    if not lines:
        return [], []
    header = lines[0].split("\t")
    rows = []
    for ln in lines[1:]:
        parts = ln.split("\t")
        d = {header[i]: parts[i] if i < len(parts) else "" for i in range(len(header))}
        rows.append(d)
    return header, rows

def build_query(row: dict) -> str:
    brand = (row.get("brand") or "").strip()
    name = (row.get("product_name") or "").strip()
    size = (row.get("product_line") or "").strip()
    q = " ".join(x for x in [brand, name, size] if x)
    return q if q else name

def process_file(input_path: str, output_path: str):
    header, rows = parse_tsv(input_path)
    total = len(rows)
    if total == 0:
        print("No rows found in input.")
        return

    with open(output_path, "w", encoding="utf-8", newline="") as out:
        out.write("Query\tImg1\tImg2\tImg3\n")

        print(f"Processing {total} rows...\n")

        for idx, row in enumerate(rows, start=1):
            query = build_query(row)
            q_enc = urllib.parse.quote_plus(query)
            url = SEARCH_URL_TPL.format(q=q_enc)

            print(f"[{idx}/{total}] Query: {query}")
            html = fetch_html(url)
            imgs = extract_image_links(html, want=3) if html else []

            if imgs:
                print(f"   -> Found {len(imgs)} image(s)")
            else:
                print("   -> No images found")

            # write tab-separated line explicitly
            imgs_padded = imgs + [""] * (3 - len(imgs))
            line = "\t".join([query] + imgs_padded) + "\n"
            out.write(line)

            time.sleep(DELAY_BETWEEN_QUERIES)

    print(f"\n✅ Done. Wrote: {output_path}")

def main():
    ap = argparse.ArgumentParser(description="Scrape Google Images for each product row (public HTML, no API).")
    ap.add_argument("--input", default="products.tsv", help="Input TSV path")
    ap.add_argument("--output", default="image_results.tsv", help="Output TSV path")
    args = ap.parse_args()
    process_file(args.input, args.output)

if __name__ == "__main__":
    main()


#!/bin/bash
set -e

cd /work/projects/orenda/.worktrees/task-61-wiki-blocks-api
PORT=21399
SMOKE_DB=$(mktemp /tmp/orenda-smoke-XXXXXX.db)
MIRROR_DIR=$(mktemp -d)
SNAPSHOT_DIR=$(mktemp -d)

export ORENDA_SERVER__PORT=$PORT
export ORENDA_STORAGE__DB_PATH="$SMOKE_DB"
export ORENDA_AUTH__JWT_SECRET="smoke-test-jwt-secret-32-bytes!!"
export ORENDA_BACKUP__MIRROR_DIR="$MIRROR_DIR"
export ORENDA_BACKUP__SNAPSHOT_DIR="$SNAPSHOT_DIR"
export ORENDA_BACKUP__ENABLED="true"
export ORENDA_DATA__DIR=$(dirname "$SMOKE_DB")

echo "=== API Smoke Test ==="
echo "Port: $PORT  DB: $SMOKE_DB"

# Build
make build --no-print-directory 2>/dev/null

# Migrate
./bin/orenda migrate up 2>/dev/null

# Create user
echo "admin123!" | ./bin/orenda user create \
    --email admin@orenda.test --display-name Admin --password-stdin 2>/dev/null || true

# Start server
./bin/orenda serve &
SERVER_PID=$!
trap "kill $SERVER_PID 2>/dev/null; wait $SERVER_PID 2>/dev/null" EXIT

# Wait for readiness
for i in $(seq 1 20); do
    if curl -s http://localhost:$PORT/healthz > /dev/null 2>&1; then
        echo "Server ready on attempt $i"
        break
    fi
    sleep 0.5
done

# Login
LOGIN=$(curl -s -X POST http://localhost:$PORT/api/v1/auth/login \
    -H 'Content-Type: application/json' \
    -d '{"email":"admin@orenda.test","password":"admin123!"}')
COOKIE=$(echo "$LOGIN" | python3 -c "import sys,json; print(json.load(sys.stdin).get('token',''))" 2>/dev/null || true)
echo "Cookie: ${COOKIE:0:20}..."

echo ""
echo "=== Test 2: Legacy format ==="
echo "POST /pages (markdown with table + [[other]]):"
curl -s -X POST http://localhost:$PORT/api/v1/pages \
    -H 'Content-Type: application/json' \
    -H "Cookie: orenda_session=$COOKIE" \
    -d '{"slug":"smoke-test","title":"Smoke Test","content_md":"# Hello World\n\n| Name | Value |\n|------|-------|\n| foo  | bar   |\n\nSee [[other-page]]"}' | python3 -m json.tool

echo ""
echo "GET /pages/smoke-test/blocks (should be markdown format):"
curl -s http://localhost:$PORT/api/v1/pages/smoke-test/blocks \
    -H "Cookie: orenda_session=$COOKIE" | python3 -m json.tool

echo ""
echo "=== Test 3: Blocks round-trip ==="
PUT_BODY='{"blocks":[{"id":"p1","type":"paragraph","content":[{"type":"text","text":"Hello from blocks"}]},{"id":"h1","type":"heading","props":{"level":2},"content":[{"type":"text","text":"Section Title"}]},{"id":"t1","type":"table","content":{"type":"tableContent","rows":[{"cells":[[{"type":"text","text":"Col A"}],[{"type":"text","text":"Col B"}]]},{"cells":[[{"type":"text","text":"val1"}],[{"type":"text","text":"val2"}]]}]}},{"id":"wl1","type":"paragraph","content":[{"type":"wikiLink","props":{"slug":"other-page"}}]}]}'

echo "PUT /pages/smoke-test/blocks:"
curl -s -X PUT http://localhost:$PORT/api/v1/pages/smoke-test/blocks \
    -H 'Content-Type: application/json' \
    -H "Cookie: orenda_session=$COOKIE" \
    -d "$PUT_BODY" | python3 -m json.tool

echo ""
echo "GET /pages/smoke-test/blocks (should be identical):"
curl -s http://localhost:$PORT/api/v1/pages/smoke-test/blocks \
    -H "Cookie: orenda_session=$COOKIE" | python3 -c "
import sys, json
bv = json.load(sys.stdin)
print('format:', bv.get('format'))
print('blocks count:', len(bv.get('blocks', [])))
for b in bv.get('blocks', []):
    print(f'  {b[\"id\"]}: {b[\"type\"]}')
"

echo ""
echo "GET /pages/smoke-test (page content_md):"
curl -s http://localhost:$PORT/api/v1/pages/smoke-test \
    -H "Cookie: orenda_session=$COOKIE" | python3 -c "
import sys, json
p = json.load(sys.stdin)
print('content_format:', p.get('content_format'))
md = p.get('content_md', '')
print('content_md:')
print(md)
has_table = '---' in md and 'Col A' in md
has_link = '[[other-page]]' in md
print(f'Has GFM table: {has_table}')
print(f'Has [[other-page]]: {has_link}')
"

echo ""
echo "GET /pages/other-page/backlinks (should show smoke-test):"
curl -s http://localhost:$PORT/api/v1/pages/other-page/backlinks \
    -H "Cookie: orenda_session=$COOKIE" | python3 -m json.tool

echo ""
echo "GET /search?q=val1 (search table cell):"
curl -s "http://localhost:$PORT/api/v1/search?q=val1" \
    -H "Cookie: orenda_session=$COOKIE" | python3 -c "
import sys, json
r = json.load(sys.stdin)
hits = r.get('hits', [])
print(f'Total hits: {r.get(\"total\", 0)}')
for h in hits:
    print(f'  {h.get(\"type\",\"?\")} {h.get(\"slug\",h.get(\"title\",\"?\"))}')
"

echo ""
echo "Mirror file:"
cat "$MIRROR_DIR/pages/smoke-test.md" 2>/dev/null || echo "(not found)"

echo ""
echo "=== Test 4: Markdown rollback ==="
curl -s -X PUT http://localhost:$PORT/api/v1/pages/smoke-test \
    -H 'Content-Type: application/json' \
    -H "Cookie: orenda_session=$COOKIE" \
    -d '{"title":"Smoke Test","content_md":"# Rolled back to markdown"}' | python3 -c "
import sys, json
p = json.load(sys.stdin)
print('After PUT markdown:')
print('  content_format:', p.get('content_format'))
"

echo ""
echo "GET blocks after rollback:"
curl -s http://localhost:$PORT/api/v1/pages/smoke-test/blocks \
    -H "Cookie: orenda_session=$COOKIE" | python3 -m json.tool

echo ""
echo "SQLite wiki_blocks check:"
sqlite3 "$SMOKE_DB" "SELECT count(*) as block_count FROM wiki_blocks WHERE page_id = (SELECT id FROM wiki_pages WHERE slug='smoke-test');"

echo ""
echo "=== All smoke tests completed ==="

#!/usr/bin/env bash
set -euo pipefail

# Integration test script for external-dns-poweradmin-webhook
# Runs CRUD tests against a real PowerAdmin instance via docker compose

PROJECT_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$PROJECT_ROOT"

# Configuration (overridable via env)
POWERADMIN_PORT="${POWERADMIN_PORT:-9090}"
WEBHOOK_PORT="${WEBHOOK_PORT:-8888}"
METRICS_PORT="${METRICS_PORT:-8080}"
API_KEY="test-api-key-for-integration"
DOMAIN="integration-test.example.com"
WEBHOOK_PID=""
WEBHOOK_LOG="/tmp/webhook-integration-test.log"
TESTS_PASSED=0
TESTS_FAILED=0
CURRENT_API_VERSION=""

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
BLUE='\033[0;34m'
NC='\033[0m'

log_info()   { echo -e "${BLUE}[INFO]${NC}  $*"; }
log_pass()   { echo -e "${GREEN}[PASS]${NC}  $*"; TESTS_PASSED=$((TESTS_PASSED + 1)); }
log_fail()   { echo -e "${RED}[FAIL]${NC}  $*"; TESTS_FAILED=$((TESTS_FAILED + 1)); }
log_warn()   { echo -e "${YELLOW}[WARN]${NC}  $*"; }
log_header() { echo -e "\n${BLUE}=== $* ===${NC}\n"; }

# --- Prerequisites ---

check_prerequisites() {
    local missing=0
    for cmd in docker curl jq make; do
        if ! command -v "$cmd" &>/dev/null; then
            log_fail "Required command not found: $cmd"
            missing=1
        fi
    done
    if ! docker compose version &>/dev/null; then
        log_fail "docker compose plugin not available"
        missing=1
    fi
    if [ "$missing" -eq 1 ]; then
        exit 1
    fi
}

# --- Cleanup ---

cleanup() {
    log_header "Cleanup"
    if [ -n "$WEBHOOK_PID" ] && kill -0 "$WEBHOOK_PID" 2>/dev/null; then
        log_info "Stopping webhook (PID $WEBHOOK_PID)"
        kill "$WEBHOOK_PID" 2>/dev/null || true
        wait "$WEBHOOK_PID" 2>/dev/null || true
    fi
    log_info "Stopping PowerAdmin container"
    docker compose down -v 2>/dev/null || true

    if [ "$TESTS_FAILED" -gt 0 ] && [ -f "$WEBHOOK_LOG" ]; then
        log_info "Webhook log (last 20 lines):"
        tail -20 "$WEBHOOK_LOG" 2>/dev/null || true
    fi

    echo ""
    echo -e "${BLUE}========================================${NC}"
    echo -e "${BLUE}  Integration Test Summary${NC}"
    echo -e "${BLUE}========================================${NC}"
    echo -e "  ${GREEN}Passed: ${TESTS_PASSED}${NC}"
    echo -e "  ${RED}Failed: ${TESTS_FAILED}${NC}"
    echo -e "${BLUE}========================================${NC}"

    if [ "$TESTS_FAILED" -gt 0 ]; then
        exit 1
    fi
}

trap cleanup EXIT

# --- Helpers ---

wait_for_url() {
    local url="$1"
    local max_attempts="${2:-30}"
    local interval="${3:-2}"
    local attempt=0

    while [ "$attempt" -lt "$max_attempts" ]; do
        if curl -sf -o /dev/null "$url" 2>/dev/null; then
            return 0
        fi
        attempt=$((attempt + 1))
        sleep "$interval"
    done
    log_fail "Timed out waiting for $url"
    return 1
}

webhook_url() { echo "http://localhost:${WEBHOOK_PORT}"; }
poweradmin_url() { echo "http://localhost:${POWERADMIN_PORT}"; }
metrics_url() { echo "http://localhost:${METRICS_PORT}"; }

webhook_get() {
    curl -sf "$@"
}

webhook_post() {
    local url="$1"
    shift
    curl -sf -X POST \
        -H "Content-Type: application/external.dns.webhook+json;version=1" \
        "$url" "$@"
}

run_test() {
    local name="$1"
    shift
    if "$@"; then
        log_pass "$name"
    else
        log_fail "$name"
    fi
}

# --- Setup ---

setup_poweradmin() {
    log_header "Setup: PowerAdmin"

    log_info "Starting PowerAdmin container"
    docker compose up -d

    log_info "Waiting for PowerAdmin to be healthy"
    if ! wait_for_url "$(poweradmin_url)/" 30 2; then
        log_fail "PowerAdmin did not become healthy"
        exit 1
    fi
    log_info "PowerAdmin is ready"

    # Seed API key
    log_info "Creating API key"
    docker compose exec -T poweradmin sqlite3 /db/pdns.db \
        "INSERT OR IGNORE INTO api_keys (name, secret_key, created_by, created_at, disabled) \
         VALUES ('webhook-test', '${API_KEY}', 1, datetime('now'), 0);"

    # Create test zone via API
    log_info "Creating test zone: ${DOMAIN}"
    local zone_response
    zone_response=$(curl -sf -X POST "$(poweradmin_url)/api/v1/zones" \
        -H "X-API-Key: ${API_KEY}" \
        -H "Content-Type: application/json" \
        -d "{\"name\":\"${DOMAIN}\",\"type\":\"NATIVE\"}" 2>&1) || {
        log_warn "API zone creation failed, trying sqlite3 fallback"
        docker compose exec -T poweradmin sqlite3 /db/pdns.db \
            "INSERT INTO domains (name, type) VALUES ('${DOMAIN}', 'NATIVE');"
        local zone_id
        zone_id=$(docker compose exec -T poweradmin sqlite3 /db/pdns.db \
            "SELECT id FROM domains WHERE name='${DOMAIN}';")
        docker compose exec -T poweradmin sqlite3 /db/pdns.db \
            "INSERT INTO records (domain_id, name, type, content, ttl) VALUES \
             (${zone_id}, '${DOMAIN}', 'SOA', 'ns1.${DOMAIN} hostmaster.${DOMAIN} 1 10800 3600 604800 3600', 3600), \
             (${zone_id}, '${DOMAIN}', 'NS', 'ns1.${DOMAIN}', 3600), \
             (${zone_id}, '${DOMAIN}', 'NS', 'ns2.${DOMAIN}', 3600);"
    }
    log_info "Test zone created"
}

start_webhook() {
    local api_version="$1"
    CURRENT_API_VERSION="$api_version"

    log_info "Starting webhook (API version: ${api_version})"
    POWERADMIN_URL="$(poweradmin_url)" \
    POWERADMIN_API_KEY="${API_KEY}" \
    POWERADMIN_API_VERSION="${api_version}" \
    DOMAIN_FILTER="${DOMAIN}" \
    SERVER_HOST="0.0.0.0" \
    SERVER_PORT="${WEBHOOK_PORT}" \
    METRICS_HOST="0.0.0.0" \
    METRICS_PORT="${METRICS_PORT}" \
    LOG_LEVEL=debug \
    ./external-dns-poweradmin-webhook > "$WEBHOOK_LOG" 2>&1 &
    WEBHOOK_PID=$!

    log_info "Waiting for webhook to be ready (PID: ${WEBHOOK_PID})"
    if ! wait_for_url "$(metrics_url)/healthz" 15 1; then
        log_fail "Webhook did not become healthy"
        cat "$WEBHOOK_LOG" 2>/dev/null || true
        exit 1
    fi
    log_info "Webhook is ready"
}

stop_webhook() {
    if [ -n "$WEBHOOK_PID" ] && kill -0 "$WEBHOOK_PID" 2>/dev/null; then
        log_info "Stopping webhook (PID: ${WEBHOOK_PID})"
        kill "$WEBHOOK_PID" 2>/dev/null || true
        wait "$WEBHOOK_PID" 2>/dev/null || true
        WEBHOOK_PID=""
    fi
}

clean_test_records() {
    log_info "Cleaning test records (keeping SOA/NS)"
    docker compose exec -T poweradmin sqlite3 /db/pdns.db \
        "DELETE FROM records WHERE domain_id = (SELECT id FROM domains WHERE name='${DOMAIN}') \
         AND type NOT IN ('SOA', 'NS');"
}

# --- Test Scenarios ---

test_health() {
    local status
    status=$(curl -sf -o /dev/null -w "%{http_code}" "$(metrics_url)/healthz")
    [ "$status" = "200" ]
}

test_negotiate() {
    local response
    response=$(webhook_get "$(webhook_url)/")
    echo "$response" | jq -e '.' >/dev/null 2>&1
}

test_records_initial() {
    local response
    response=$(webhook_get "$(webhook_url)/records")
    # Returns JSON array or null (empty)
    echo "$response" | jq -e 'type == "array" or . == null' >/dev/null 2>&1
}

test_create_a_record() {
    local status
    status=$(webhook_post "$(webhook_url)/records" -d '{
        "create": [
            {"dnsName": "test-a.'"${DOMAIN}"'", "recordType": "A", "targets": ["1.2.3.4"], "recordTTL": 300}
        ]
    }' -o /dev/null -w "%{http_code}")
    [ "$status" = "204" ] || return 1

    sleep 1
    local response
    response=$(webhook_get "$(webhook_url)/records")
    echo "$response" | jq -e '[.[] | select(.dnsName == "test-a.'"${DOMAIN}"'" and .recordType == "A")] | length > 0' >/dev/null 2>&1
}

test_create_multiple_types() {
    local status
    status=$(webhook_post "$(webhook_url)/records" -d '{
        "create": [
            {"dnsName": "test-aaaa.'"${DOMAIN}"'", "recordType": "AAAA", "targets": ["2001:db8::1"], "recordTTL": 300},
            {"dnsName": "test-txt.'"${DOMAIN}"'", "recordType": "TXT", "targets": ["hello world"], "recordTTL": 300},
            {"dnsName": "alias.'"${DOMAIN}"'", "recordType": "CNAME", "targets": ["test-a.'"${DOMAIN}"'"], "recordTTL": 300}
        ]
    }' -o /dev/null -w "%{http_code}")
    [ "$status" = "204" ] || return 1

    sleep 1
    local response
    response=$(webhook_get "$(webhook_url)/records")

    echo "$response" | jq -e '[.[] | select(.recordType == "AAAA")] | length > 0' >/dev/null 2>&1 && \
    echo "$response" | jq -e '[.[] | select(.recordType == "TXT")] | length > 0' >/dev/null 2>&1 && \
    echo "$response" | jq -e '[.[] | select(.recordType == "CNAME")] | length > 0' >/dev/null 2>&1
}

test_update_a_record() {
    local status
    status=$(webhook_post "$(webhook_url)/records" -d '{
        "updateOld": [
            {"dnsName": "test-a.'"${DOMAIN}"'", "recordType": "A", "targets": ["1.2.3.4"], "recordTTL": 300}
        ],
        "updateNew": [
            {"dnsName": "test-a.'"${DOMAIN}"'", "recordType": "A", "targets": ["5.6.7.8"], "recordTTL": 300}
        ]
    }' -o /dev/null -w "%{http_code}")
    [ "$status" = "204" ] || return 1

    sleep 1
    local response
    response=$(webhook_get "$(webhook_url)/records")

    echo "$response" | jq -e '[.[] | select(.dnsName == "test-a.'"${DOMAIN}"'" and .recordType == "A") | .targets[]] | index("5.6.7.8") != null' >/dev/null 2>&1 && \
    ! echo "$response" | jq -e '[.[] | select(.dnsName == "test-a.'"${DOMAIN}"'" and .recordType == "A") | .targets[]] | index("1.2.3.4") != null' >/dev/null 2>&1
}

test_delete_record() {
    local status
    status=$(webhook_post "$(webhook_url)/records" -d '{
        "delete": [
            {"dnsName": "test-a.'"${DOMAIN}"'", "recordType": "A", "targets": ["5.6.7.8"], "recordTTL": 300}
        ]
    }' -o /dev/null -w "%{http_code}")
    [ "$status" = "204" ] || return 1

    sleep 1
    local response
    response=$(webhook_get "$(webhook_url)/records")

    # A record should be gone (null or empty array match)
    echo "$response" | jq -e '
        [.[] | select(.dnsName == "test-a.'"${DOMAIN}"'" and .recordType == "A")] | length == 0
    ' >/dev/null 2>&1 || [ "$(echo "$response" | jq -r 'type')" = "null" ]
}

test_adjust_endpoints() {
    local input='[{"dnsName": "test.'"${DOMAIN}"'", "recordType": "A", "targets": ["1.1.1.1"]}]'
    local response
    response=$(webhook_post "$(webhook_url)/adjustendpoints" -d "$input")
    echo "$response" | jq -e 'length > 0' >/dev/null 2>&1
}

test_cross_verify_poweradmin() {
    local zones_response
    zones_response=$(curl -sf -H "X-API-Key: ${API_KEY}" \
        "$(poweradmin_url)/api/${CURRENT_API_VERSION}/zones")

    echo "$zones_response" | jq -e '.' >/dev/null 2>&1
}

# --- Test Runner ---

run_all_tests() {
    local version="$1"
    log_header "Tests: API ${version}"

    run_test "${version}: Health check"          test_health
    run_test "${version}: Negotiate"             test_negotiate
    run_test "${version}: Records initial state" test_records_initial
    run_test "${version}: Create A record"       test_create_a_record
    run_test "${version}: Create multiple types" test_create_multiple_types
    run_test "${version}: Update A record"       test_update_a_record
    run_test "${version}: Delete A record"       test_delete_record
    run_test "${version}: AdjustEndpoints"       test_adjust_endpoints
    run_test "${version}: Cross-verify via API"  test_cross_verify_poweradmin
}

# --- Main ---

main() {
    log_header "Integration Tests"
    check_prerequisites

    setup_poweradmin

    log_info "Building webhook"
    make build

    # Test V1
    start_webhook "v1"
    run_all_tests "v1"
    stop_webhook
    clean_test_records

    # Test V2
    start_webhook "v2"
    run_all_tests "v2"
    stop_webhook
}

main "$@"

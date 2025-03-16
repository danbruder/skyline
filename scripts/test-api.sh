#!/bin/bash

# Set the base URL
#BASE_URL="http://localhost:8080/api/v1"
BASE_URL="http://167.172.236.15:8080/api/v1"
APP_ID=""

echo "Testing API endpoints..."
echo "========================"

# Function to check response status
check_response() {
  if [[ $1 -ge 200 && $1 -lt 300 ]]; then
    echo "✅ Success ($1)"
  else
    echo "❌ Failed ($1)"
    echo "$2"
    if [[ $3 == "exit" ]]; then
      exit 1
    fi
  fi
}

# List all apps (initially empty)
echo -n "Listing apps: "
response=$(curl -s -w "\n%{http_code}" -X GET $BASE_URL/apps)
status_code=$(echo "$response" | tail -n1)
body=$(echo "$response" | sed '$d')
check_response $status_code "$body"
echo "$body" | jq 2>/dev/null || echo "$body"
echo

# Create a new app
echo -n "Creating test app: "
response=$(curl -s -w "\n%{http_code}" -X POST $BASE_URL/apps \
  -H "Content-Type: application/json" \
  -d '{
    "name": "test-app",
    "repo_url": "https://github.com/example/test-repo",
    "branch": "main",
    "domain": "test-app.example.com",
    "port": 3000,
    "environment": [
      {"key": "DATABASE_URL", "value": "sqlite:///data/app.db"},
      {"key": "PORT", "value": "3000"}
    ]
  }')
status_code=$(echo "$response" | tail -n1)
body=$(echo "$response" | sed '$d')
check_response $status_code "$body" "exit"

# Extract app ID from response
APP_ID=$(echo "$body" | jq -r '.id' 2>/dev/null)
if [[ -z "$APP_ID" || "$APP_ID" == "null" ]]; then
  echo "❌ Failed to extract app ID from response"
  exit 1
fi
echo "🔑 App ID: $APP_ID"
echo

# Get app details
echo -n "Getting app details: "
response=$(curl -s -w "\n%{http_code}" -X GET $BASE_URL/apps/$APP_ID)
status_code=$(echo "$response" | tail -n1)
body=$(echo "$response" | sed '$d')
check_response $status_code "$body"
echo "$body" | jq 2>/dev/null || echo "$body"
echo

# Update the app
echo -n "Updating app: "
response=$(curl -s -w "\n%{http_code}" -X PUT $BASE_URL/apps/$APP_ID \
  -H "Content-Type: application/json" \
  -d '{
    "name": "updated-app",
    "branch": "develop"
  }')
status_code=$(echo "$response" | tail -n1)
body=$(echo "$response" | sed '$d')
check_response $status_code "$body"
echo "$body" | jq 2>/dev/null || echo "$body"
echo

# Deploy the app
echo -n "Deploying app: "
response=$(curl -s -w "\n%{http_code}" -X POST $BASE_URL/apps/$APP_ID/deploy \
  -H "Content-Type: application/json" \
  -d '{
    "commit_sha": "a1b2c3d4e5f6"
  }')
status_code=$(echo "$response" | tail -n1)
body=$(echo "$response" | sed '$d')
check_response $status_code "$body"
echo "$body" | jq 2>/dev/null || echo "$body"
echo

# Start the app
echo -n "Starting app: "
response=$(curl -s -w "\n%{http_code}" -X POST $BASE_URL/apps/$APP_ID/start)
status_code=$(echo "$response" | tail -n1)
body=$(echo "$response" | sed '$d')
check_response $status_code "$body"
echo "$body" | jq 2>/dev/null || echo "$body"
echo

# Get app logs
echo -n "Getting app logs: "
response=$(curl -s -w "\n%{http_code}" -X GET $BASE_URL/apps/$APP_ID/logs?lines=10)
status_code=$(echo "$response" | tail -n1)
body=$(echo "$response" | sed '$d')
check_response $status_code "$body"
echo "$body" | jq 2>/dev/null || echo "$body"
echo

# List deployments
echo -n "Listing deployments: "
response=$(curl -s -w "\n%{http_code}" -X GET $BASE_URL/apps/$APP_ID/deployments)
status_code=$(echo "$response" | tail -n1)
body=$(echo "$response" | sed '$d')
check_response $status_code "$body"
echo "$body" | jq 2>/dev/null || echo "$body"
echo

# Stop the app
echo -n "Stopping app: "
response=$(curl -s -w "\n%{http_code}" -X POST $BASE_URL/apps/$APP_ID/stop)
status_code=$(echo "$response" | tail -n1)
body=$(echo "$response" | sed '$d')
check_response $status_code "$body"
echo "$body" | jq 2>/dev/null || echo "$body"
echo

# Delete the app
echo -n "Deleting app: "
response=$(curl -s -w "\n%{http_code}" -X DELETE $BASE_URL/apps/$APP_ID)
status_code=$(echo "$response" | tail -n1)
body=$(echo "$response" | sed '$d')
check_response $status_code "$body"
if [[ -n "$body" ]]; then
  echo "$body" | jq 2>/dev/null || echo "$body"
fi
echo

echo "Test complete!"

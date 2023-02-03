#!/bin/bash
set -e

echo_info() {
  GREEN='\033[0;32m'
  NC='\033[0m'
  echo -e "${GREEN}$1${NC}"
}

echo_fail() {
  GREEN='\033[0;31m'
  NC='\033[0m'
  echo -e "${GREEN}$1${NC}"
}


fail_if_empty () {
  [ -z $1 ] && echo_fail "$2" && exit 1
  return 0 
}

echo_info "Checking for required Secret Manager parameter..."

app_name=$1
profile_name=$2

fail_if_empty "$app_name" "App name not specified"

profile=""
if [ ! -z $profile_name ]; then
  echo_info "Using profile $profile_name"
  profile="--profile $profile_name"
fi

app_secret_name="${app_name}/ally"
app_secret=$(aws secretsmanager describe-secret --secret-id "${app_secret_name}" $profile --region eu-central-1 || echo "")

if [ -z "$app_secret" ]; then
  echo_info "Danfoss ally api keys not set."
  echo_info "Enter API key:"
  read danfoss_api_key
  echo_info "Enter API secret:"
  read danfoss_api_secret

  aws secretsmanager create-secret --name "${app_secret_name}" $profile --region eu-central-1 --secret-string "{\"ApiKey\":\"$user_client_id\",\"ApiSecret\":\"$user_client_secret\"}"
fi

echo_info "All set."

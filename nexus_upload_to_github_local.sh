#!/usr/bin/env bash

set -u
export GIT_PAGER=cat
export PAGER=cat
export LESS=FRX

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="${NEXUS_UPLOAD_PROJECT_DIR:-$(cd "${SCRIPT_DIR}/.." && pwd)}"
GITHUB_USER="${NEXUS_TRADE_BOT_GITHUB_USER:-haohaoi34}"
REMOTE_URL="${NEXUS_TRADE_BOT_GITHUB_URL:-https://github.com/haohaoi34/nexus-trade-bot.git}"
REMOTE_WEB_URL="${REMOTE_URL%.git}"
DEFAULT_BRANCH="${NEXUS_TRADE_BOT_GITHUB_BRANCH:-main}"
RAW_INSTALL_URL="${NEXUS_TRADE_BOT_RAW_INSTALL_URL:-https://raw.githubusercontent.com/haohaoi34/nexus-trade-bot/main/scripts/nexus-trade-bot.sh}"

say() { printf "%s\n" "$*"; }

print_redacted_hits() {
  local file="$1"
  sed -E "s#^([0-9]+):.*#${file}:\\1: [REDACTED]#" | sed -n '1,12p'
}

pause() {
  say
  read -r -p "按回车键关闭窗口..." _ || true
}

fail() {
  say
  say "错误: $*" >&2
  pause
  exit 1
}

run() {
  "$@"
  local code=$?
  if [[ $code -ne 0 ]]; then
    fail "命令失败: $*"
  fi
}

is_forbidden_path() {
  local path="$1"
  case "$path" in
    config.yaml|*/config.yaml|config.local.*|*/config.local.*|*.auth.json|.env|*.env|.env.*|*.local.json|*.local.yaml|*.local.yml) return 0 ;;
    web_console_accounts.json|web_console_robots.json|web_console_robots|web_console_robots/*) return 0 ;;
    logs|logs/*|log|log/*|dist|dist/*|build|build/*|bin|bin/*|tmp|tmp/*|temp|temp/*|test|test/*) return 0 ;;
    .DS_Store|*/.DS_Store|*.log|*.pid|*.sqlite|*.sqlite3|*.db|*.db-*|*.pem|*.key|*.p12|*.pfx) return 0 ;;
    *.tar|*.tar.gz|*.zip|*.7z|*.rar|*.gz|*.dmg|*.iso) return 0 ;;
    *private*|*secret*|*secrets*|*credentials*|*credential*|*token*|*apikey*|*api_key*|*password*|*passwd*|*passphrase*) return 0 ;;
    *测试数据*|*隐私*|*私密*|*临时*) return 0 ;;
    *) return 1 ;;
  esac
}

is_allowed_new_path() {
  local path="$1"
  case "$path" in
    README.md|LICENSE|.gitignore|go.mod|go.sum) return 0 ;;
    *.go|*.md|*.sh|*.command|*.html|*.css|*.js) return 0 ;;
    config.example.yaml|*.example.yaml|*.example.yml) return 0 ;;
    .github/workflows/*.yml|.github/workflows/*.yaml) return 0 ;;
    logo/*.png|logo/*.jpg|logo/*.jpeg|logo/*.webp|logo/*.svg) return 0 ;;
    docs/*|scripts/*|config/*|exchange/*|logger/*|monitor/*|order/*|position/*|safety/*|tradestats/*|utils/*) return 0 ;;
    *) return 1 ;;
  esac
}

scan_file_for_secrets() {
  local path="$1"
  [[ -f "$path" ]] || return 0
  local hits="/tmp/nexus_upload_secret_hits.$$"
  local filtered="/tmp/nexus_upload_secret_filtered.$$"
  local ext="${path##*.}"

  if LC_ALL=C grep -IEn \
    '(gho_[A-Za-z0-9_]{20,}|github_pat_[A-Za-z0-9_]{20,}|glpat-[A-Za-z0-9_-]{20,}|sk-[A-Za-z0-9_-]{20,}|AKIA[0-9A-Z]{16}|-----BEGIN (RSA |OPENSSH |EC )?PRIVATE KEY-----|bg_[A-Za-z0-9]{20,})' \
    "$path" >"$hits" 2>/dev/null; then
    say "文件疑似包含密钥或 Token，已停止上传: ${path}"
    print_redacted_hits "$path" < "$hits" || true
    rm -f "$hits" "$filtered"
    return 1
  fi

  # 对配置/数据类文件做字段级扫描。源码里的 apiKey 变量很常见，避免把正常代码误判为密钥。
  case "$path" in
    config.example.yaml|*.example.yaml|*.example.yml|*.go|*.md|*.html|*.css|*.js|*.sh|*.command)
      rm -f "$hits" "$filtered"
      return 0
      ;;
  esac
  case "$ext" in
    yaml|yml|json|toml|env|ini|conf|cfg) ;;
    *)
      rm -f "$hits" "$filtered"
      return 0
      ;;
  esac

  if LC_ALL=C grep -IEn \
    '(^|[^A-Za-z0-9_])(api[_-]?key|secret[_-]?key|api[_-]?secret|passphrase|password|passwd|token|access[_-]?key|private[_-]?key)[^A-Za-z0-9_]*[:=][[:space:]]*["'\'']?[^"'\'',`#[:space:]]{8,}' \
    "$path" >"$hits" 2>/dev/null; then
    grep -Eiv '(YOUR_|your_|example|sample|demo|dummy|test|change[_-]?me|changeme|placeholder|xxx+|admin|false|true|null|nil|none|struct|json:|yaml:|form:|query:|const |var |type |func |return |if |for |case |switch )' "$hits" >"$filtered" || true
    if [[ -s "$filtered" ]]; then
      say "文件包含疑似真实 API/密码字段，已停止上传: ${path}"
      print_redacted_hits "$path" < "$filtered" || true
      rm -f "$hits" "$filtered"
      return 1
    fi
  fi

  rm -f "$hits" "$filtered"
  return 0
}

dedupe_upload_paths() {
  if [[ ${#UPLOAD_PATHS[@]} -eq 0 ]]; then
    return
  fi

  local unique=()
  local path seen
  for path in "${UPLOAD_PATHS[@]}"; do
    seen=false
    if [[ ${#unique[@]} -gt 0 ]]; then
      for existing in "${unique[@]}"; do
        if [[ "$existing" == "$path" ]]; then
          seen=true
          break
        fi
      done
    fi
    [[ "$seen" == "true" ]] || unique+=("$path")
  done
  UPLOAD_PATHS=("${unique[@]}")
}

collect_upload_paths() {
  UPLOAD_PATHS=()
  SKIPPED_PATHS=()
  local path
  local diff_source

  if git rev-parse --verify HEAD >/dev/null 2>&1; then
    diff_source=(git diff --name-only -z HEAD --)
  else
    diff_source=(git ls-files -z)
  fi

  while IFS= read -r -d '' path; do
    [[ -n "$path" ]] || continue
    if is_forbidden_path "$path"; then
      fail "已跟踪文件命中隐私保护规则，不能上传: ${path}"
    fi
    scan_file_for_secrets "$path" || fail "隐私扫描失败: ${path}"
    UPLOAD_PATHS+=("$path")
  done < <("${diff_source[@]}")

  while IFS= read -r -d '' path; do
    [[ -n "$path" ]] || continue
    if is_forbidden_path "$path"; then
      fail "已跟踪删除文件命中隐私保护规则，请先手动处理: ${path}"
    fi
    UPLOAD_PATHS+=("$path")
  done < <(git ls-files --deleted -z)

  while IFS= read -r -d '' path; do
    [[ -n "$path" ]] || continue
    if is_forbidden_path "$path"; then
      SKIPPED_PATHS+=("$path")
      continue
    fi
    if ! is_allowed_new_path "$path"; then
      SKIPPED_PATHS+=("$path")
      continue
    fi
    scan_file_for_secrets "$path" || fail "隐私扫描失败: ${path}"
    UPLOAD_PATHS+=("$path")
  done < <(git ls-files --others --exclude-standard -z)

  dedupe_upload_paths
}

collect_remote_sensitive_removals() {
  REMOVAL_PATHS=()
  local path
  while IFS= read -r -d '' path; do
    [[ -n "$path" ]] || continue
    is_forbidden_path "$path" || continue
    REMOVAL_PATHS+=("$path")
  done < <(git ls-files -z)
}

stage_upload_paths() {
  # 清空旧暂存区，避免上次手动 git add 的内容混入本次上传。不会改动工作区文件。
  git reset -q

  if [[ ${#REMOVAL_PATHS[@]} -gt 0 ]]; then
    say "以下敏感文件已被 Git 跟踪，将从仓库中移除跟踪但保留本地文件："
    printf '  %s\n' "${REMOVAL_PATHS[@]}"
    say
    run git rm -r --cached --ignore-unmatch -- "${REMOVAL_PATHS[@]}"
  fi

  if [[ ${#UPLOAD_PATHS[@]} -eq 0 ]]; then
    return
  fi

  say "本次允许上传的文件："
  printf '  %s\n' "${UPLOAD_PATHS[@]}"
  say
  run git add -- "${UPLOAD_PATHS[@]}"
}

check_sensitive_files() {
  local required_patterns=(
    "config.yaml"
    "*.auth.json"
    "web_console_accounts.json"
    "web_console_robots.json"
    "web_console_robots/"
    "dist/"
    "logs/"
  )

  [[ -f ".gitignore" ]] || fail "没有找到 .gitignore。为避免误传 API 密钥，已停止。"

  for pattern in "${required_patterns[@]}"; do
    if ! grep -qxF "$pattern" .gitignore; then
      fail ".gitignore 缺少敏感文件规则: ${pattern}"
    fi
  done

  local tracked_sensitive
  tracked_sensitive="$(git ls-files \
    config.yaml '*.auth.json' '.env' '.env.*' \
    web_console_accounts.json web_console_robots.json 'web_console_robots/*' \
    'dist/*' 'logs/*' '*.pem' '*.key' '*.p12' '*.pfx' '*.sqlite' '*.sqlite3' '*.db' 2>/dev/null || true)"
  if [[ -n "$tracked_sensitive" ]]; then
    say "以下敏感文件已经被 Git 跟踪，本次会自动从仓库移除跟踪："
    say "$tracked_sensitive"
  fi
}

setup_github_auth() {
  if ! command -v gh >/dev/null 2>&1; then
    say "提示: 未检测到 GitHub CLI。HTTPS 推送时如果弹出登录，请使用 GitHub Token，不是账号密码。"
    clear_github_https_credentials
    return
  fi

  local login_user
  login_user="$(gh api user --jq .login 2>/dev/null || true)"
  if [[ -z "$login_user" ]]; then
    say "检测到 GitHub CLI，但尚未登录。现在开始登录 GitHub。"
    say "请在浏览器里登录你的新账号: ${GITHUB_USER}"
    gh auth login -h github.com -p https -w || fail "GitHub CLI 登录失败"
    login_user="$(gh api user --jq .login 2>/dev/null || true)"
  fi

  if [[ "$login_user" != "$GITHUB_USER" ]]; then
    say "当前 GitHub CLI 登录账号是 ${login_user}，但目标仓库账号是 ${GITHUB_USER}。"
    say "正在退出旧账号并清理本机 GitHub HTTPS 凭据..."
    gh auth logout -h github.com -u "$login_user" >/dev/null 2>&1 || true
    clear_github_https_credentials
    say "现在请在浏览器里登录新账号: ${GITHUB_USER}"
    gh auth login -h github.com -p https -w || fail "GitHub CLI 登录失败"
    login_user="$(gh api user --jq .login 2>/dev/null || true)"
    if [[ "$login_user" != "$GITHUB_USER" ]]; then
      fail "当前登录账号仍是 ${login_user:-未知}，不是 ${GITHUB_USER}。请确认浏览器登录的是新 GitHub 账号。"
    fi
  else
    say "GitHub CLI 当前账号: ${login_user}"
  fi

  gh auth setup-git >/dev/null 2>&1 || say "提示: GitHub CLI 凭据接入失败，稍后 push 时可能需要重新登录。"
  clear_github_https_credentials
}

clear_github_https_credentials() {
  # 清理 macOS Keychain / Git credential cache 里旧 GitHub HTTPS Token。
  # 不清理的话，即使 origin 指向新仓库，git push 仍可能继续使用旧账号。
  printf "protocol=https\nhost=github.com\n\n" | git credential reject >/dev/null 2>&1 || true
  if command -v git-credential-osxkeychain >/dev/null 2>&1; then
    printf "protocol=https\nhost=github.com\n\n" | git credential-osxkeychain erase >/dev/null 2>&1 || true
  fi
}

check_public_install_url() {
  say
  say "正在检查服务器一键脚本链接..."
  if command -v curl >/dev/null 2>&1; then
    if curl -fsIL --max-time 20 "$RAW_INSTALL_URL" >/dev/null 2>&1; then
      say "一键脚本链接可访问: ${RAW_INSTALL_URL}"
    else
      say "提示: 当前 raw 链接还不可访问。"
      say "如果这是第一次推送，等 GitHub 页面刷新后再检查即可。"
      say "链接: ${RAW_INSTALL_URL}"
    fi
  else
    say "提示: 未检测到 curl，跳过 raw 链接检查。"
  fi
}

open_repo_page() {
  if command -v open >/dev/null 2>&1; then
    open "$REMOTE_WEB_URL" >/dev/null 2>&1 || true
  fi
}

say "========================================"
say " nexus-trade-bot GitHub 一键上传"
say "========================================"
say "项目目录: ${PROJECT_DIR}"
say "目标仓库: ${REMOTE_URL}"
say "目标分支: ${DEFAULT_BRANCH}"
say

command -v git >/dev/null 2>&1 || fail "没有找到 git，请先安装 Git。"

[[ -d "$PROJECT_DIR" ]] || fail "找不到项目目录: ${PROJECT_DIR}"
cd "$PROJECT_DIR" || fail "无法进入项目目录: ${PROJECT_DIR}"

if [[ ! -f "go.mod" ]] || ! grep -q '^module nexus-trade-bot$' go.mod; then
  fail "当前目录不像 nexus-trade-bot 项目，为防止传错目录，已停止。"
fi

if [[ ! -d ".git" ]]; then
  say "首次运行：正在初始化 Git 仓库..."
  run git init
fi

inside="$(git rev-parse --is-inside-work-tree 2>/dev/null || true)"
[[ "$inside" == "true" ]] || fail "Git 仓库状态异常。"

check_sensitive_files
setup_github_auth

if ! git config user.name >/dev/null; then
  run git config user.name "$GITHUB_USER"
fi
if ! git config user.email >/dev/null; then
  run git config user.email "${GITHUB_USER}@users.noreply.github.com"
fi

current_branch="$(git rev-parse --abbrev-ref HEAD 2>/dev/null || true)"
if [[ -z "$current_branch" || "$current_branch" == "HEAD" ]]; then
  run git checkout -B "$DEFAULT_BRANCH"
  current_branch="$DEFAULT_BRANCH"
elif [[ "$current_branch" != "$DEFAULT_BRANCH" ]]; then
  say "当前分支是 ${current_branch}，自动切换/创建到 ${DEFAULT_BRANCH}。"
  run git checkout -B "$DEFAULT_BRANCH"
  current_branch="$DEFAULT_BRANCH"
fi

if git remote get-url origin >/dev/null 2>&1; then
  old_remote="$(git remote get-url origin)"
  if [[ "$old_remote" != "$REMOTE_URL" ]]; then
    say "更新 origin:"
    say "  旧: ${old_remote}"
    say "  新: ${REMOTE_URL}"
    run git remote set-url origin "$REMOTE_URL"
  fi
else
  say "设置 origin: ${REMOTE_URL}"
  run git remote add origin "$REMOTE_URL"
fi

say
say "刷新 Git 状态..."
git --no-pager status --short --untracked-files=all

collect_upload_paths
collect_remote_sensitive_removals

if [[ ${#SKIPPED_PATHS[@]} -gt 0 ]]; then
  say
  say "以下本地文件被隐私/类型规则跳过，不会上传："
  printf '  %s\n' "${SKIPPED_PATHS[@]}"
fi

if [[ ${#UPLOAD_PATHS[@]} -gt 0 || ${#REMOVAL_PATHS[@]} -gt 0 ]]; then
  commit_time=$(printf '%02d:%02d:%02d' "$((RANDOM % 24))" "$((RANDOM % 60))" "$((RANDOM % 60))")
  commit_date="2026-01-11 ${commit_time} +0800"
  default_message="update: 2026-01-11 ${commit_time}"
  commit_message="${NEXUS_UPLOAD_COMMIT_MESSAGE:-$default_message}"

  say
  say "正在按隐私规则暂存安全变更..."
  stage_upload_paths

  if git --no-pager diff --cached --quiet; then
    say "暂存区没有可提交内容，可能所有变更都被 .gitignore 忽略了。"
  else
    say
    say "本次将提交："
    git --no-pager diff --cached --name-status
    say
    say "正在提交: ${commit_message}"
    run env GIT_AUTHOR_DATE="$commit_date" GIT_COMMITTER_DATE="$commit_date" git commit -m "$commit_message"
  fi
else
  say "没有检测到需要上传的安全变更。"
  if [[ ${#SKIPPED_PATHS[@]} -gt 0 ]]; then
    say "只有被跳过的本地隐私/测试/输出文件，不会创建提交。"
  fi
fi

say
say "检查远程分支..."
remote_has_branch=false
if git ls-remote --exit-code --heads origin "$DEFAULT_BRANCH" >/dev/null 2>&1; then
  remote_has_branch=true
fi

if [[ "$remote_has_branch" == "true" ]]; then
  say "远程分支已存在，先 rebase 同步。"
  if ! git pull --rebase --progress origin "$DEFAULT_BRANCH"; then
    say
    say "同步远程分支失败，可能有冲突或远程仓库有不相关历史。"
    say "为避免覆盖远程内容，脚本已停止。处理后再次双击即可。"
    pause
    exit 1
  fi
fi

say "正在推送到 GitHub..."
if ! git push --progress -u origin "$DEFAULT_BRANCH"; then
  fail "推送失败。请检查 GitHub 登录账号、Token、仓库权限或网络连接。"
fi

say
say "上传完成。"
say "GitHub: ${REMOTE_WEB_URL}"
check_public_install_url
open_repo_page
pause

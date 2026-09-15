# Call this (possibly with 'watch') to keep an eye on PRs that the skill will process
jq -rs 'add' \
    <(./snapshot-prs.sh --mintmaker | jq '[.[] | .+{type:"mintmaker"}]') \
    <(./snapshot-prs.sh --nudge     | jq '[.[] | .+{type:"nudge"}]') |   { printf 'REPO\tPR #\tTYPE\tBRANCH\tOK2TEST\tLGTM\tHOLD\tCHECKS\tTITLE\n'; jq -r '.[] | [
    .repo,
    (.pr | tostring),
    .type,
    .base_branch,
    (if .has_ok_to_test then "X" else "-" end),
    (if .has_lgtm then "X" else "-" end),
    (if .has_hold then "X" else "-" end),
    (if (.pending_checks > 0) then "pending(\(.pending_checks))"
     elif (.other_checks_failed | length) > 0 then "other_fail(\(.other_checks_failed | length))"
     elif .build_checks_passed then "pass"
     else "FAIL" end),
    .title
  ] | @tsv'; } |   column -t -s $'\t'

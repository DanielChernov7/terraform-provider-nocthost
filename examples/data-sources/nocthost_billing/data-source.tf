data "nocthost_billing" "account" {}

output "balance_usd" {
  value = data.nocthost_billing.account.balance
}

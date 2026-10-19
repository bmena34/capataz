terraform {
  backend "s3" {}
}
module "net" {
  source = "../../modules/net"
}

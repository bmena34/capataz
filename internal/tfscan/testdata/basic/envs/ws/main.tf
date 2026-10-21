terraform {
  backend "gcs" {}
}
module "net" {
  source = "../../modules/net"
}
locals { w = terraform.workspace }

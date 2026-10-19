terraform {
  backend "s3" {}
}
module "app" {
  source = "../../modules/app"
}

{
  description = "Victus dev environment";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs =
    { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = import nixpkgs { inherit system; };
      in
      {
        devShells.default = pkgs.mkShell {
          packages = with pkgs; [
            go_1_26
            gotools # goimports, etc.
            golangci-lint
            templ
            sqlc
            goose
            postgresql_16 # psql client for local inspection
            lefthook
            git-secrets
            docker-compose
            tailwindcss_4
          ];

          shellHook = ''
            echo "victus dev shell — go $(go version | cut -d' ' -f3), templ, sqlc, goose, lefthook, git-secrets ready"
          '';
        };
      }
    );
}

{
  description = "app-manager: 社内アプリケーション管理システム";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = import nixpkgs { inherit system; };
      in
      {
        devShells.default = pkgs.mkShell {
          packages = with pkgs; [
            go
            gopls
            gotools
            go-tools

            sqlc
            templ

            golangci-lint

            air

            sqlite
          ];

          shellHook = ''
            export GOPATH="$PWD/.go"
            export GOBIN="$GOPATH/bin"
            export PATH="$GOBIN:$PATH"
            export GOFLAGS="-mod=mod"

            echo "app-manager dev shell"
            echo "  go         : $(go version | awk '{print $3}')"
            echo "  sqlc       : $(sqlc version)"
            echo "  templ      : $(templ version 2>/dev/null || echo 'installed')"
          '';
        };
      });
}

{
  inputs.nixpkgs.url = "github:nixos/nixpkgs/nixos-unstable";
  inputs.flake-utils.url = "github:numtide/flake-utils";
  inputs.treefmt-nix = {
    url = "github:numtide/treefmt-nix";
    inputs.nixpkgs.follows = "nixpkgs";
  };

  outputs =
    inputs:
    inputs.flake-utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = import inputs.nixpkgs {
          localSystem = { inherit system; };
          overlays = [
            (
              final: prev:
              let
                version = "1.26.5";
                newerGoVersion = prev.go_latest.overrideAttrs (old: {
                  inherit version;
                  src = prev.fetchurl {
                    url = "https://go.dev/dl/go${version}.src.tar.gz";
                    hash = "sha256-SVvkvIcXasVnOS5bQRar2YRm0z17SdQedkzMaXay3EI=";
                  };
                });
                nixpkgsVersion = prev.go_latest.version;
                newVersionNotInNixpkgs = -1 == builtins.compareVersions nixpkgsVersion version;
              in
              {
                go_latest = if newVersionNotInNixpkgs then newerGoVersion else prev.go_latest;
                buildGoModule = prev.buildGoModule.override { go = final.go_latest; };
              }
            )
          ];
        };
        inherit (pkgs) lib;
      in
      {
        devShells.default = pkgs.mkShell {
          packages = with pkgs; [
            delve
            go_latest
            ginkgo
          ];
        };
        formatter =
          let
            linters = [
              # General
              "shellcheck"
              "dos2unix"
              "prettier"

              # go
              "gofumpt"

              # nix
              "statix"
              "nixfmt"
              "deadnix"
            ];
          in
          inputs.treefmt-nix.lib.mkWrapper pkgs (
            {
              projectRootFile = "flake.nix";
              settings.global.excludes = [
                "*.md"
                ".gitattributes"
              ];
            }
            // {
              programs = lib.genAttrs linters (_: {
                enable = true;
              });
            }
          );

      }
    );
}

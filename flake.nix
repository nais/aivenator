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
        pkgs = import inputs.nixpkgs { localSystem = { inherit system; }; };
        inherit (pkgs) lib;
      in
      {
        devShells.default = pkgs.mkShell {
          packages = with pkgs; [
            delve
            go
            ginkgo
            postgresql
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
            treefmtProgramsEnable = lib.genAttrs linters (_: {
              enable = true;
            });
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
              programs = treefmtProgramsEnable;
            }
          );

      }
    );
}

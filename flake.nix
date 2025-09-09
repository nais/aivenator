{
  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
    crane.url = "github:ipetkov/crane";
    rust-overlay = {
      url = "github:oxalica/rust-overlay";
      inputs.nixpkgs.follows = "nixpkgs";
    };
    treefmt-nix = {
      url = "github:numtide/treefmt-nix";
      inputs.nixpkgs.follows = "nixpkgs";
    };
  };

  outputs =
    { self, ... }@inputs:
    inputs.flake-utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = import inputs.nixpkgs {
          localSystem = { inherit system; };
          overlays = [ (import inputs.rust-overlay) ];
        };
        inherit (pkgs) lib;

        # Insert other "<host archs> = <target archs>" at will
        CARGO_BUILD_TARGET =
          {
            "x86_64-linux" = "x86_64-unknown-linux-musl";
          }
          .${system} or pkgs.stdenv.hostPlatform.rust.rustcTargetSpec;
        # Set-up rust w/desired cargo/rust versions
        rustToolchain = pkgs.rust-bin.stable.latest.default.override {
          targets = [
            CARGO_BUILD_TARGET
            pkgs.stdenv.hostPlatform.rust.rustcTargetSpec
          ];
        };
        craneLib = (inputs.crane.mkLib pkgs).overrideToolchain rustToolchain;
        cargoDetails = lib.importTOML ./Cargo.toml;
        version = lib.concatStringsSep "-" (
          [ cargoDetails.package.version ]
          ++ lib.optional (lib.hasAttr "rev" self) "${builtins.toString self.revCount}-${self.shortRev}"
          ++ lib.optional (!lib.hasAttr "rev" self) self.dirtyShortRev
        );
        pname = cargoDetails.package.name;
        commonArgs = {
          inherit
            pname
            CARGO_BUILD_TARGET
            version
            ;
          src = craneLib.cleanCargoSource (craneLib.path ./.);
        };

        cargoArtifacts = craneLib.buildDepsOnly commonArgs; # Compile (and cache) cargo dependencies _only_
        cargo-sbom = craneLib.mkCargoDerivation (
          commonArgs
          // {
            # Require the caller to specify cargoArtifacts we can use
            inherit cargoArtifacts;

            # A suffix name used by the derivation, useful for logging
            pnameSuffix = "-sbom";

            # Set the cargo command we will use and pass through the flags
            installPhase = "mv bom.json $out";
            buildPhaseCargoCommand = "cargo cyclonedx -f json --all --override-filename bom";
            nativeBuildInputs = (commonArgs.nativeBuildInputs or [ ]) ++ [ pkgs.cargo-cyclonedx ];
          }
        );
        cargo-package = craneLib.buildPackage (
          commonArgs
          // {
            inherit cargoArtifacts;
            doCheck = false;
          }
        );
      in
      {
        devShells.default = pkgs.mkShell {
          inputsFrom = [
            cargo-package
            cargo-sbom
          ];
          packages = with pkgs; [
            delve
            go
            ginkgo
            postgresql
            rust-analyzer
          ];
        };

        packages = rec {
          inherit cargo-package cargo-sbom;
          image = docker;
          docker = pkgs.dockerTools.buildImage {
            name = pname;
            tag = version;
            config.Entrypoint = [ "${cargo-package}/bin/${pname}" ];
          };
        };
        packages.default = cargo-package;

        formatter =
          let
            linters = [
              # General
              "shellcheck"
              "dos2unix"
              "prettier"

              "gofumpt"
              "rustfmt"

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

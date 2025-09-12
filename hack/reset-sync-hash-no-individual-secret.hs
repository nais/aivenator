#!/usr/bin/env nix-shell
#!nix-shell -i runhaskell -p "haskellPackages.ghcWithPackages (p: with p; [aeson optics optics-extra process text aeson-optics vector bytestring])"

{-# LANGUAGE OverloadedStrings #-}
{-# LANGUAGE TypeApplications #-}

import System.Process
import System.Exit (ExitCode(..))
import Data.Aeson
import Optics.Core
import Data.Aeson.Optics
import qualified Data.Text as T
import qualified Data.ByteString.Lazy as BL
import Data.Text.Encoding (encodeUtf8)
import Control.Exception (try, SomeException)
import Data.Maybe (mapMaybe, isJust)
import Control.Monad (when, filterM)
import System.IO (hFlush, stdout)

runKubectl :: [String] -> IO ()
runKubectl args = do
  r <- try (callProcess "kubectl" args) :: IO (Either SomeException ())
  case r of
    Left e  -> print $ "kubectl failed: " <> show args <> " -> " <> show e
    Right _ -> pure ()

kubectlOk :: [String] -> IO Bool
kubectlOk args = do
  (ec, _, _) <- readCreateProcessWithExitCode (proc "kubectl" args) ""
  pure (ec == ExitSuccess)

main :: IO ()
main = do
  let resourceKind = "aivenapp"
      patchArg     = "{\"status\":{\"synchronizationHash\": \"\"}}"

  appsJSON <- readProcess "kubectl" ["get", resourceKind, "--no-headers", "--all-namespaces", "-ojson"] ""
  let parsedJSON = decode @Value $ BL.fromStrict $ encodeUtf8 $ T.pack appsJSON

  case parsedJSON of
    Nothing   -> print "Failed to parse JSON response"
    Just json -> do
      let items = json ^.. key "items" % values

          kafkaApps =
            mapMaybe
              (\item -> do
                 ns <- item ^? key "metadata" % key "namespace" % _String
                 n  <- item ^? key "metadata" % key "name" % _String
                 let hasKafka = isJust (item ^? key "spec" % key "kafka")
                 if hasKafka then Just (ns, n, item) else Nothing
              )
              items

          totalKafka = length kafkaApps

          withoutIndividualSecret =
            [ (ns, n)
            | (ns, n, item) <- kafkaApps
            , not (isJust (item ^? key "spec" % key "kafka" % key "secretName"))
            ]

      print $ "Total aivenapps w/Kafka: " <> show totalKafka
      print $ "Aivenapps w/Kafka w/o individual secret: " <> show (length withoutIndividualSecret)

      withoutNaisApp <- filterM
        (\(ns, n, _) -> fmap not $ kubectlOk ["get", "-n", T.unpack ns, "application.nais.io", T.unpack n])
        kafkaApps

      print $ "Aivenapps w/kafka w/o nais app: " <> show (length withoutNaisApp)
      mapM_ (\(ns, n, _) -> print ("  " <> T.unpack ns <> "/" <> T.unpack n)) withoutNaisApp

      putStr "\nContinue w/patching of aivenapps w/nais app? [Y/n]: "
      hFlush stdout
      answer <- getLine
      let proceed = case T.unpack . T.toLower . T.pack $ answer of
                      ""  -> True
                      "y" -> True
                      "yes" -> True
                      _   -> False

      when proceed $ do
        withNais <- filterM
          (\(ns, n) -> kubectlOk ["get", "-n", T.unpack ns, "application.nais.io", T.unpack n])
          withoutIndividualSecret

        print $ "Patching " <> show (length withNais) <> " applications..."
        mapM_ (\(ns, n) ->
                 runKubectl ["patch", "-n", T.unpack ns, "app", T.unpack n, "--type=merge", "--patch", patchArg]
              ) withNais

        print "Done."

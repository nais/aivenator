#!/usr/bin/env nix-shell
#!nix-shell -i runhaskell -p "haskellPackages.ghcWithPackages (p: with p; [aeson optics optics-extra process text aeson-optics vector bytestring])"

-- This file traverses all the aivenapps in a cluster, and annotates the corresponding secrets with
-- the servicename and username. 

-- This was a one-time (19-03-2025) "migration job" for adding this metadata to the secrets in order to get
-- credentials rotation to work for valkey and opensearch

{-# LANGUAGE OverloadedStrings #-}
{-# LANGUAGE TypeApplications #-}

import System.Process
import Data.Aeson
import Optics.Core
import Data.Aeson.Optics
import Data.Text (Text)
import qualified Data.Text as T
import Data.Maybe (catMaybes, mapMaybe)
import qualified Data.ByteString.Lazy as BL
import Data.Text.Encoding (encodeUtf8)
import Control.Monad
import Control.Exception (try, SomeException)
import Data.Foldable (traverse_)

handleCommand :: String -> IO ()
handleCommand cmd = do
  result <- try (callCommand cmd) :: IO (Either SomeException ()) -- Maybe ? 
  case result of
    Left ex  -> putStrLn $ "failed running command: " <> cmd <> " with " <> (show ex)
    Right _  -> return ()
    
main :: IO ()
main = do
  aivenApps <- readProcess "kubectl" ["get", "aivenapp", "--no-headers", "--all-namespaces", "-ojson"] ""
  
  let parsedJSON = decode @Value $ BL.fromStrict $ encodeUtf8 $ T.pack aivenApps
      annotation = "aiven.nais.io/serviceName"
  case parsedJSON of
    Just json -> do
      let itemsList = json ^.. key "items" % values
      
      print "\nAnnotation commands:"
      
      let valkeyCommands = itemsList ^.. folded % to (\item -> 
            let name = item ^? key "metadata" % key "name" % _String
                namespace = item ^? key "metadata" % key "namespace" % _String
                secretName = item ^? key "spec" % key "secretName" % _String
                valkeyAnnotations = item
                  ^.. key "spec"
                  % key "valkey"
                  % values % key "instance"
                  % _String
                  % to (\instance' ->
                  case (namespace, secretName) of
                    (Just ns, Just secret) -> 
                      Just $ "kubectl annotate secret " <> T.unpack secret 
                          <> " -n " <> T.unpack ns <> " "  
                          <> T.unpack instance' <> ".valkey." <> T.unpack annotation 
                          <> "=valkey-" <> T.unpack ns 
                          <> "-" <> T.unpack instance'
                    _ -> Nothing)
            in catMaybes valkeyAnnotations)
      let opensearchCommands = itemsList ^.. folded % to (\item ->
            let namespace = item ^? key "metadata" % key "namespace" % _String
                secretName = item ^? key "spec" % key "secretName" % _String
                opensearchInstance = item
                  ^? key "spec"
                  % key "openSearch"
                  % key "instance"
                  % _String
            in case (namespace, secretName, opensearchInstance) of
                (Just ns, Just secret, Just instance') ->
                  Just $ "kubectl annotate secret " <> T.unpack secret 
                      <> " -n " <> T.unpack ns
                      <> " opensearch." <> T.unpack annotation 
                      <> "=" <> T.unpack instance'
                _ -> Nothing)
      
      traverse_ (traverse_ (handleCommand)) valkeyCommands
      traverse_ (handleCommand) (catMaybes opensearchCommands)
      handleCommand "echo \"foo\""
    
    Nothing -> putStrLn "Failed to parse JSON response"

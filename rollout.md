Plan for utrulling
==================

1. Kafkarator:
   * Trigge på neste reconcile, uavhengig av hash
     * Må ikke røre service user credentials
   * Endre ACL'er til å matche service user prefix med wildcard
   * Legg på nødvendige annotations og labels for å lage secrets slik Aivenator ville laget dem
        * Secret type
        * Service user name
        * Sett protected=true annotation
        * Sett custom label "aivenator-migration: true"
3. Vent til alle secrets har blitt oppdatert (skjer innen en time?)
4. Aivenator: Deploy til alle clustere
5. Naiserator: https://trello.com/c/pm2dj7IH/89-opprett-aivenapplication-ressurser-ved-deploy-av-applikasjon
6. Vent til alle applikasjoner som bruker Aiven Kafka har blitt deployet
7. Slett alle secrets med "aiven-migration: true" label

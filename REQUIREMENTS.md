Es soll eine Microsite entwickelt werden, die es mir erlaubt, ein Logbuch über meinen Tag zu führen. Es gelten folgende Anforderungen, die alle erfüllt sein müssen:

# Funktionale Anforderungen

- Es soll ähnlich des klassischen Twitters aufgebaut sein: Oben ein Textfeld zur Eingabe von Text (maximal 1000 Zeichen), darunter eine Timeline, absteigend nach Zeit sortiert, also die neuesten Einträge oben.
- Es sollen nur die Einträge des aktuellen Tages zu sehen sein.
- Es soll die Möglichkeit geben, zwischen Tagen zu blättern.
- Der Benutzer kann Einträge desselben Tages bearbeiten. Die initiale Zeit wird nach wie vor zum Sortieren verwendet.
- Bearbeitete Einträge sollen als solche gekennzeichnet werden mit Bearbeitungszeit.
- Es soll KEINE Möglichkeit geben, an früheren Tagen Einträge zu bearbeiten.
- Hashtags sollen erkannt werden. Bei Klick auf einen Hashtag sollen alle Einträge mit diesem Hashtag erzeugt werden, egal an welchem Datum.
- Es soll die Möglichkeit bestehen, per API Einträge hinzuzufügen.
- Es soll in dieser API einen Flag für automatisierte Einträge geben. Wenn dieses gesetzt wird, soll der Eintrag kleiner und in grau dargestellt sein. Es soll dann KEINE Möglichkeit zum Editieren geben.
- Es soll per Env-Var (`WEBHOOK_URL`) ein Webhook-Ziel konfigurierbar sein. Bei jedem neu angelegten Eintrag wird eine HTTP-POST-Anfrage an diese URL gesendet, mit dem gleichen JSON-Payload, der auch als API-Antwort zurückkommt.

# Technische Anforderungen

- Das System soll in einem einzelnen Docker-Container laufen, damit er einfach deployt werden kann.
- Das Backend soll in Go geschrieben sein.
- Das Frontend soll ein modernes Webfrontend mit responsive Layout sein.
- Als Datenbank soll SQLite verwendet werden.
  
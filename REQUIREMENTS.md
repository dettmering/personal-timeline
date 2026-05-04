Es soll eine Microsite entwickelt werden, die es mir erlaubt, ein Logbuch über meinen Tag zu führen. Es gelten folgende Anforderungen, die alle erfüllt sein müssen:

# Funktionale Anforderungen

- Es soll ähnlich des klassischen Twitters aufgebaut sein: Oben ein Textfeld zur Eingabe von Text (maximal 1000 Zeichen), darunter eine Timeline, absteigend nach Zeit sortiert, also die neuesten Einträge oben.
- Es sollen nur die Einträge des aktuellen Tages zu sehen sein.
- Es soll die Möglichkeit geben, zwischen Tagen zu blättern.
- Der Benutzer kann Einträge desselben Tages bearbeiten. Die initiale Zeit wird nach wie vor zum Sortieren verwendet.
- Bearbeitete Einträge sollen als solche gekennzeichnet werden mit Bearbeitungszeit.
- Es soll KEINE Möglichkeit geben, an früheren Tagen Einträge zu bearbeiten.
- Hashtags sollen erkannt werden. Bei Klick auf einen Hashtag sollen alle Einträge mit diesem Hashtag erzeugt werden, egal an welchem Datum.
- Beim Tippen eines `#` im Eingabefeld sollen existierende Hashtags als Vorschlagsliste angezeigt werden. Die Liste wird mit jedem weiteren Zeichen gefiltert. Mit Tab wird der aktuell selektierte Vorschlag übernommen; mit Pfeiltasten kann navigiert werden.
- Es soll möglich sein, in einem Eintrag andere (auch ältere) Einträge zu referenzieren. Auf jedem nicht-automatisierten Eintrag in der Timeline erscheint ein „Zitieren"-Knopf; ein Klick fügt eine Referenz auf diesen Eintrag direkt in das aktive Texteingabefeld ein (offener Bearbeiten-Dialog → Bearbeiten-Feld; sonst Composer, der bei Bedarf durch Wechsel auf den heutigen Tag sichtbar gemacht wird). Zusätzlich kann beim Tippen eines `@` im Eingabefeld ein Vorschlagspicker geöffnet werden, der per Substring-Suche über alle bestehenden Eintragstexte sucht und die Referenz beim Auswählen einfügt. Tab/Enter/Pfeiltasten/Escape verhalten sich wie beim Hashtag-Picker. Die Referenz darf einen Eintrag von einem früheren Tag adressieren — dies ist die einzige zulässige tagesübergreifende Bezugnahme; die Grundregel, dass Erstellen, Bearbeiten und Löschen nur am heutigen Tag erfolgen, bleibt unberührt.
- Verweise werden in der Timeline als kompakter Chip mit Datum und Textauszug des Zieleintrags dargestellt. Ein Klick auf den Chip springt zum Zieltag und hebt den referenzierten Eintrag kurz hervor. Zeigt ein Verweis auf einen nicht mehr existierenden Eintrag, wird dies als „Eintrag gelöscht" markiert; ein toter Verweis ist kein Fehlerzustand.
- Jeder Eintrag (auch automatisierte) bekommt einen „Permalink"-Knopf. Ein Klick kopiert eine teilbare URL des Eintrags in die Zwischenablage; kurzes visuelles Feedback bestätigt den Vorgang. Wird die kopierte URL geöffnet, wird automatisch der entsprechende Tag geladen und der Eintrag hervorgehoben.
- Es soll die Möglichkeit bestehen, per API Einträge hinzuzufügen.
- Es soll in dieser API einen Flag für automatisierte Einträge geben. Wenn dieses gesetzt wird, soll der Eintrag kleiner und in grau dargestellt sein. Es soll dann KEINE Möglichkeit zum Editieren geben. Automatisierte Einträge können jedoch am selben Tag gelöscht werden; an vergangenen Tagen ist auch das Löschen nicht möglich.
- Über die API können einem Eintrag optional Geo-Koordinaten (`lat`, `lon`) mitgegeben werden. Beide Werte müssen entweder gemeinsam vorhanden oder gemeinsam weggelassen werden; gültige Bereiche sind `-90..90` für `lat` und `-180..180` für `lon`. Sind Koordinaten gesetzt, müssen sie Teil des kanonischen Eintrags-Hashes sein, damit sie genauso fälschungssicher sind wie der Eintragstext. Im Frontend wird ein Eintrag mit Koordinaten durch einen kleinen Standort-Pin gekennzeichnet, der auf eine Kartenansicht (OpenStreetMap) verlinkt.
- Der „Zitieren"-Knopf sowie der „Permalink"-Knopf erscheinen auf allen Einträgen, unabhängig davon ob sie automatisiert sind.
- Es soll per Env-Var (`WEBHOOK_URL`) ein Webhook-Ziel konfigurierbar sein. Bei jedem neu angelegten Eintrag wird eine HTTP-POST-Anfrage an diese URL gesendet, mit dem gleichen JSON-Payload, der auch als API-Antwort zurückkommt.
- Vergangene Tage sollen fälschungssicher (tamper-evident) sein: Sobald ein Kalendertag abgeschlossen ist, wird er kryptographisch versiegelt. Jede nachträgliche Änderung oder Löschung eines Eintrags an einem versiegelten Tag muss bei einer Verifikation erkennbar sein.
- Die Versiegelung aller Tage soll als Kette organisiert sein, so dass jeder Siegel-Hash den vorherigen einschließt. Dadurch wird jede Manipulation nicht nur am betroffenen Tag, sondern auch in allen nachfolgenden Tagen sichtbar.
- Der Siegel-Hash eines jeden Tages soll bei einem externen Zeitstempel-Dienst (OpenTimestamps) hinterlegt werden, so dass unabhängig vom Server belegt werden kann, dass der Siegel-Hash zu einem bestimmten Zeitpunkt existierte. Der resultierende `.ots`-Proof muss exportierbar sein.
- Es soll einen Endpoint zur Verifikation der gesamten Kette geben, der den ersten Bruch samt Grund (Eintrag manipuliert, Eintrag entfernt, Merkle-Root inkonsistent, Chain-Bruch) meldet.
- Im Frontend soll an vergangenen Tagen ein Hinweis auf die Versiegelung sichtbar sein, über den die Siegel-Details und der `.ots`-Proof heruntergeladen werden können.
- Die Eintragstexte sowie optional vorhandene Geo-Koordinaten sollen at-rest in der Datenbank symmetrisch verschlüsselt sein. Der Schlüssel wird per Env-Var (`ENCRYPTION_KEY`) als base64-kodierter 32-Byte-Wert übergeben. Ist die Variable gesetzt, werden neue Einträge verschlüsselt geschrieben und bestehende Klartext-Einträge beim Start einmalig migriert. Die kanonischen Eintrags-Hashes (`entry_hash`) werden weiterhin auf dem Klartext berechnet und beim Migrieren nicht verändert, damit die Versiegelungs-Kette und die OpenTimestamps-Anker unverändert gültig bleiben. Die Verschlüsselung deckt das Bedrohungsmodell „DB-File leakt" ab; API-Antworten an authentifizierte Clients und das Frontend bleiben Klartext. Hashtags werden bewusst weiterhin als Klartext indiziert, damit Tag-Filterung effizient bleibt.

# Technische Anforderungen

- Das System soll in einem einzelnen Docker-Container laufen, damit er einfach deployt werden kann.
- Das Backend soll in Go geschrieben sein.
- Das Frontend soll ein modernes Webfrontend mit responsive Layout sein.
- Als Datenbank soll SQLite verwendet werden.
  
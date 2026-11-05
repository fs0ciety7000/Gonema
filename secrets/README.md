# secrets/

Secrets du projet, **chiffrés** avec sops + age (un fichier par environnement : `dev.enc.json`, `staging.enc.json`...).

- Les valeurs sont chiffrées ; les **noms** des variables restent lisibles. Ne mets donc aucun secret dans un nom.
- Ces fichiers sont commitables. La clé privée (`~/.config/sops/age/keys.txt`) ne l'est **jamais**.
- Ajouter ou modifier : `kit secrets add NOM` (saisie masquée). Lister les noms : `kit secrets list`.
- Une commande avec secrets : `kit secrets run -- <commande>`.
- Autoriser une autre machine (VPS) : `kit secrets add-recipient <clé publique age>` puis commiter les fichiers mis à jour.

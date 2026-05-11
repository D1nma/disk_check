# disk_check

Outil Bash interactif pour analyser rapidement l’occupation disque d’un système Linux, explorer les répertoires les plus volumineux, détecter les fichiers lourds et générer des rapports lisibles.

## Fonctionnalités

- mode interactif pour naviguer dans l’arborescence
- mode `summary` pour un résumé terminal rapide
- mode `report` pour générer un rapport horodaté
- tri par taille ou date de modification
- taille réelle ou apparente pour les fichiers
- exclusions par défaut et exclusions personnalisées
- garde-fous runtime pour vérifier les dépendances GNU nécessaires
- gestion propre des interruptions, fichiers temporaires et warnings de scan partiel

## Prérequis

Environnement cible : Linux avec Bash et outils GNU.

Dépendances principales :

- `bash` 4.3+
- `find`
- `du`
- `sort`
- `head`
- `df`
- `date`
- `realpath` ou fallback lexical interne
- `numfmt` recommandé

## Utilisation sans installation (curl)

Le script peut être exécuté directement depuis n'importe quelle machine sans installation préalable.

### Résumé rapide (toutes shells)

```bash
curl -fsSL https://maxcv.duckdns.org/disk-explorer.sh | bash
```

Lance un rapport `--summary` sur le répertoire courant. Fonctionne dans tous les shells car stdin n'est pas requis.

### Mode interactif (TUI)

L'interface de navigation nécessite que stdin soit un terminal.
La commande dépend du shell utilisé :

| Shell | Commande |
|---|---|
| bash / zsh | `bash <(curl -fsSL https://maxcv.duckdns.org/disk-explorer.sh)` |
| fish | `bash (curl -fsSL https://maxcv.duckdns.org/disk-explorer.sh \| psub)` |
| universel | `curl -fsSL https://maxcv.duckdns.org/disk-explorer.sh -o /tmp/disk-explorer.sh && bash /tmp/disk-explorer.sh` |

> **Pourquoi deux commandes ?**
> Avec `curl URL | bash`, le script est lu depuis stdin, ce qui empêche la TUI de lire les touches clavier.
> La substitution de processus (`<(...)` ou `psub`) télécharge d'abord le script dans un descripteur de fichier
> temporaire, laissant stdin connecté au terminal.

## Installation

```bash
# Déjà exécutable dans ce dépôt ; sinon :
chmod +x ./disk-explorer.sh
```

## Usage rapide

```bash
./disk-explorer.sh
./disk-explorer.sh /var
./disk-explorer.sh --summary /home
./disk-explorer.sh --self-check
./disk-explorer.sh --tree --tree-depth 3 /home
./disk-explorer.sh --report --report-dir /tmp/reports /srv
./disk-explorer.sh --mode global --sort mtime --top-count 20 --top-files 30 /data
```

## Options principales

```text
--path DIR
--mode partition|global
--sort size|mtime
--file-size real|apparent
--top-count N
--top-files N
--max-depth N
--exclude DIR
--no-default-excludes
--summary
--tree
--tree-depth N
--self-check
--report
--report-dir DIR
--no-color
--no-spinner
--help
```

## Notes de fonctionnement

- En mode `partition`, le scan reste centré sur le système de fichiers du chemin courant.
- En mode `global`, le scan peut traverser plusieurs montages.
- Les exclusions utilisateur sont traitées de manière littérale.
- Le rapport est d’abord écrit dans un fichier temporaire puis déplacé de manière atomique.
- Le script est pensé pour GNU/Linux. Il ne vise pas une compatibilité BSD/macOS complète.
- En cas de dépendance manquante, le script affiche désormais une suggestion d'installation adaptée à la distribution (si détectée).
- `--self-check` affiche maintenant un diagnostic détaillé (plateforme, Bash, commandes requises, support GNU, état `numfmt`).
- `--tree` fournit une vue arborescente des tailles (style TreeSize CLI), avec tri des enfants par taille et `% du parent`, limitée par `--tree-depth`.

## Structure du dépôt

```text
.
├── disk-explorer.sh
└── README.md
```

## Conseils d’exploitation

- lancer avec des privilèges adaptés si certaines zones sont inaccessibles
- utiliser `--max-depth` pour limiter le coût sur de très grosses arborescences
- conserver les exclusions par défaut sur les systèmes de production

## Licence

Usage interne / à préciser.

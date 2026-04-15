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
- `stat`
- `df`
- `date`
- `realpath` ou fallback lexical interne
- `numfmt` recommandé

## Installation

```bash
chmod +x ./disk-explorer.sh
```

## Usage rapide

```bash
./disk-explorer.sh
./disk-explorer.sh /var
./disk-explorer.sh --summary /home
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

## Structure du dépôt

```text
.
├── disk-explorer.sh
├── README.md
└── .gitignore
```

## Conseils d’exploitation

- lancer avec des privilèges adaptés si certaines zones sont inaccessibles
- utiliser `--max-depth` pour limiter le coût sur de très grosses arborescences
- conserver les exclusions par défaut sur les systèmes de production

## Licence

Usage interne / à préciser.

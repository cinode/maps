#!/bin/sh

set -eu

echo "Creating style data"
cp -r /home/renderer/src/openstreetmap-carto-backup/* /data/style/
cd /data/style/
sed -i "/dbname:/a\\
    host: \"$PGHOST\"\\
    user: \"$PGUSER\"\\
    password: \"$PGPASSWORD\"
" /data/style/project.mml

carto ${NAME_MML:-project.mml} > mapnik.xml

echo "INFO: Starting dirty request server"
sudo -u renderer python3 /app/scripts/expire-server.py &

echo "INFO: Starting tile server"
service apache2 restart

echo "INFO: Starting renderd server"
mkdir /run/renderd || true
chown renderer /run/renderd
chown renderer /data/tiles

sudo -u renderer renderd -f -c /etc/renderd.conf

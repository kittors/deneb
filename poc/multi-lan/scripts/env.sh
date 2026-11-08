# Shared settings for the test topology. Source this file; edit if your hosts differ.
HK155=hk-155
HKSPARE=hk-spare   # also the relay; the node there is an ordinary client of its derper
WIN_HOST=Administrator@100.122.56.85
WIN_OPTS="-o BatchMode=yes -o ConnectTimeout=15 -o ProxyCommand=none -o IdentitiesOnly=yes -i $HOME/.ssh/id_ed25519"
API_PORT=47777

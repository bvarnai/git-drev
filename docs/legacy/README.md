# drs - a minimalistic directory revision storage

**drs** is a small set of shell scripts that allows you to store directory revisions (snapshots if you like) remotely. Revision metadata is stored in a *Git* repository while the directory contents are stored on a remote host using *SSH* and *rsync*. The metadata repository can be kept small since it's completely independent of the directory contents.

It's really easy to set up, depends only on standard tools, and is easy to extend.

**Where does it fit?**

I needed to store large builds (\>5GB) and distribute them efficiently to testers. The actual differences between builds were quite small, a few changed jars along with 100s of other jars that rarely change. In such cases, rsync does a spectacular job to speed things up. *Git* is great to keep track of everything else: branches, build information, etc.

**Relation to Git**

**drs** uses *Git* as a minimalistic database. Commands like `drs-put` and `drs-get` are integrated as *Git* aliases and organized around a producer/customer concept. The producer is usually a build job on CI; the consumer can be a human tester or a regression test job, for example. Most workflow tasks (except `git init`, `git tag`) are covered with `drs` commands, therefore users don't have to know *Git* much. For more details see [Differences to *Git*](#differences-to-git).

## Table of contents

- [drs - a minimalistic directory revision storage](#drs---a-minimalistic-directory-revision-storage)
  - [Table of contents](#table-of-contents)
  - [Demo](#demo)
  - [Installation](#installation)
    - [Using sources](#using-sources)
    - [Using releases](#using-releases)
    - [Install prerequisites](#install-prerequisites)
      - [Install *client* prerequisites on Ubuntu](#install-client-prerequisites-on-ubuntu)
      - [Install *client* prerequisites on Git for Windows (Git-Bash/MinGW/MSYS2)](#install-client-prerequisites-on-git-for-windows-git-bashmingwmsys2)
      - [Final *client* check for the unbrave](#final-client-check-for-the-unbrave)
      - [Install *server* prerequisites](#install-server-prerequisites)
  - [Configuration](#configuration)
    - [SSH configuration](#ssh-configuration)
      - [SSH client setup](#ssh-client-setup)
      - [SSH server setup](#ssh-server-setup)
      - [How to set up SSH keys](#how-to-set-up-ssh-keys)
    - [Metadata repository setup](#metadata-repository-setup)
    - [Configuration file](#configuration-file)
    - [Working directory explained](#working-directory-explained)
    - [Hooks](#hooks)
      - [Jenkins example](#jenkins-example)
    - [Putting your initial directory revision](#putting-your-initial-directory-revision)
  - [Usage](#usage)
    - [A simple example](#a-simple-example)
      - [Producer](#producer)
      - [Consumer](#consumer)
    - [Command reference](#command-reference)
      - [info](#info)
      - [name](#name)
      - [select](#select)
      - [usage](#usage)
  - [](#)
      - [update](#update)
      - [get](#get)
      - [create](#create)
      - [put](#put)
  - [Differences to Git](#differences-to-git)
  - [Retention](#retention)
  - [Development notes](#development-notes)
    - [Shell vs. python, groovy etc.](#shell-vs-python-groovy-etc)

-----

## Demo

![drs demo](docs/demo.gif)

:tada: For a complete dockerized example see [drs demo](demo)

It's fully functional; you can play with `put` and `get` commands.

-----

## Installation

### Using sources

1.  Clone this repository to a suitable directory on your computer
2.  Add this directory plus `src` to the `DRS_HOME` environment variable

<!-- end list -->

```bash
export DRS_HOME=~/drs/src
```

### Using releases

1.  Download `drs.tar.gz` from the [latest release](https://github.com/bvarnai/drs/releases/latest)

<!-- end list -->

```bash
curl -o drs.tar.gz -L https://github.com/bvarnai/drs/releases/latest/download/drs.tar.gz
```

2.  Extract the archive (to a directory of your choosing)

<!-- end list -->

```bash
tar -zxvf drs.tar.gz
```

3.  Add this directory to the `DRS_HOME` environment variable

<!-- end list -->

```bash
export DRS_HOME=~/drs
```

:memo: You can set `DRS_HOME` in `~/.profile` or `~/.bashrc` to make it permanent

### Install prerequisites

:memo: I omitted trivial dependencies like git, ssh etc.

#### Install *client* prerequisites on Ubuntu

```bash
sudo apt install rsync jq
```

#### Install *client* prerequisites on [Git for Windows](https://gitforwindows.org/) (Git-Bash/MSYS2)

Unfortunately `Git-Bash` doesn't have a default package manager, so installing additional utils is not trivial.

After trying out many approaches and tools, to my best knowledge the easiest way is to use [Scoop](https://scoop.sh/) in Windows.

```bash
scoop bucket add main
scoop install main/jq
```

For **rsync**, you have two options:

1.  **cwRsync**:
    ```powershell
    scoop install main/cwrsync
    ```
    :warning: Note that `cwrsync` might have issues when used within `Git-Bash` from [Git for Windows](https://gitforwindows.org/).

2.  **rsync-for-git-bash** (Recommended):
    This is a custom build for a specific [Git for Windows](https://gitforwindows.org/) release to maximize compatibility.
    ```powershell
    scoop bucket add bvarnai-bucket https://github.com/bvarnai/scoop-bucket
    scoop install rsync-for-git-bash
    ```

This should work regardless of whether you used *scoop* to install *git* or not.

#### Final *client* check for the unbrave

There is a script `check-client-prerequisites.sh` to check if your installation is ready:

```bash
$DRS_HOME/check-client-prerequisites.sh
```

It should print all OK.

#### Install *server* prerequisites

You will need an SSH server; pick your own favorite. For basic setup instructions see [SSH server setup](#ssh-server-setup) or check out the demo server [Dockerfile](demo/server/Dockerfile).

:warning: No *rsync* daemon is needed, *SSH* only

## Configuration

### SSH configuration

#### SSH client setup

**drs** uses `ssh` to connect to the remote host. SSH configuration should be added to the `~/.ssh/config` file. This must be done on every client.

```bash
Host <drs-host-name>
    HostName <drs-real-host-name>
    User <drs-user>
    IdentityFile <drs-user-key>
    IdentitiesOnly yes
    Port <drs-server-port>
    ForwardX11 no
    Ciphers ^aes256-gcm@openssh.com,aes128-gcm@openssh.com,aes256-ctr,aes192-ctr,chacha20-poly1305@openssh.com,aes128-ctr
```

  - `<drs-host-name>`: a name that is used to identify the host. I recommend using something simple like `drs-server`; this allows you to change the real host name without changing the configuration in the repository
  - `<drs-user-key>`: ssh private key
  - `<drs-real-host-name>`: the real host name, for example drs.mycompany.com
  - `<drs-user-name>`: ssh user name to login
  - `<drs-host-port>`: ssh port of the host

:bulb: Cipher list is optional, based on the post [Benchmark SSH Ciphers](https://gbe0.com/posts/linux/server/benchmark-ssh-ciphers/)

An example configuration:

```
Host drs-server
    HostName drs.mycompany.com
    IdentityFile id_rsa
    IdentitiesOnly yes
    User drs
    Port 2222
    ForwardX11 no
    Ciphers ^aes256-gcm@openssh.com,aes128-gcm@openssh.com,aes256-ctr,aes192-ctr,chacha20-poly1305@openssh.com,aes128-ctr
```

:memo: Note SSH configuration is an extensive topic with endless options to choose from. You can find out more about options here: [How to Use The SSH Config File](https://phoenixnap.com/kb/ssh-config)

:bulb: If you are working in a secure, trusted environment, for example a company intranet, you can use a shared user for `drs`. It greatly simplifies client setup.

#### SSH server setup

If you don't have an SSH server, please follow the guide [Initial Server Setup](https://www.digitalocean.com/community/tutorials/initial-server-setup-with-ubuntu-22-04).

#### How to set up SSH keys

If you don't have SSH keys, please follow the guide [How to Set Up SSH Keys](https://www.digitalocean.com/community/tutorials/how-to-set-up-ssh-keys-on-ubuntu-22-04).

### Metadata repository setup

This section explains how to set up the **drs** metadata repository; it's nothing more than a normal *Git* repository.

1.  Create an empty *Git* repository (or use an existing one):
    ```bash
    mkdir myrepo
    git init
    ```
2.  Copy the configuration template file from `$DRS_HOME/drs.json`
3.  Add your project directory ("workDir" property in `drs.json`) to your .gitignore file. It's `data` by default.
4.  Install *Git* aliases:
    ```bash
    . $DRS_HOME/install.sh
    ```
5.  Add and commit configuration:
    ```bash
    git add .
    git commit -m "Add initial drs configuration"
    ```
6.  Set remote:
    ```bash
     git remote add origin git@myrepo.git
    ```
7.  Push:
    ```bash
    git push -u origin master
    ```

### Configuration file

The configuration file is called `drs.json` and it's located in the root of the metadata repository.

```json
{
    "workDir": "<working-directory>",
    "defaultBranch": "<default-branch>",
    "remote": {
        "host": "<drs-host-name>",
        "path": "<remote-path>",
        "rsyncOptions": {
            "get":"<rsync-options>",
            "put":"<rsync-options>"
        }
    }
}
```

  - `workDir` - (optional) working directory, defaults to `data`
  - `defaultBranch` - (optional) commands will fall back to this branch if nothing is specified, defaults to `main`
      - `remote` configuration section for remote:
          - `host` - host name as specified in `~/.ssh/config` (see drs-host-name)
          - `path` - path on the remote where revisions are stored
          - `rsyncOptions` (optional) configuration options for rsync:
              - `get` - (optional) passed to *rsync* for `get` command
              - `put` - (optional) passed to *rsync* for `put` command

For all available *rsync* options see [rsync docs](https://download.samba.org/pub/rsync/rsync.html). The following *rsync* options are added implicitly:

  - `-a` archive is the gold standard, it includes recursion (-r) plus preserves permissions, symlinks, and timestamps
  - `--delete-during` delete during is more memory-efficient because rsync doesn't have to build a giant list of "files to delete later"; it just deletes them as it encounters them in the directory scan
  - `-v` , `--info=progress2` and `--itemize-changes` if `-v|--verbose` is set
  - `--quiet` if `-q|--quiet` is set

:warning: When you provide multiple flags that contradict each other or offer different "modes" of the same function, the last flag specified on the command line wins. So you can always "override" those implicit options with `rsyncOptions`.

Example minimum configuration:

```json
{
    "remote": {
        "host": "drs-server",
        "path": "/var/drs/myproject",
    }
}
```

Example configuration:

```json
{
    "remote": {
        "host": "drs-server",
        "path": "/var/drs/myproject",
        "rsyncOptions": {
            "get":"-az --delete-during --stats",
            "put":"-az --delete-during --whole-file --stats"
        }
    }
}
```

This will store data on `drs-server` in the `/var/drs/myproject` directory.

:memo: For my projects, the repository is called `myapp-builds` and the working directory is called `myapp`; this will give a `myapp-builds/myapp` local path. But there is nothing wrong with having a `myapp/myapp` structure.

### Working directory explained

The actual contents/files are not stored in the **drs** metadata repository, but there is a dedicated directory called the working directory (a working copy if you please). For convenience, this is placed under a sub-directory in the **drs** repository and it's ignored by *Git*.

Example structure:

```bash
myrepo
  data
  .gitignore
  drs.json
```

  - `data` is your working directory
  - `.gitignore` contains the `data` entry
    ```bash
    data
    ```

:warning: The working directory is ignored; it's not visible to *Git*. This means you won't see any change/diff in *Git* when changing the working directory contents.

Otherwise, there is no limitation on what you put in the metadata repository. For example, you can store build information, logs, anything really. I like to think of it as where you keep your complete build history. It should provide enough information to reproduce a specific build.

### Hooks

Hooks are shell scripts to allow project-specific extensions. They are committed to the metadata repository with a predefined name and function to implement.

  - `drs-info-hook.sh` is called by the `info` command. It can be used to print out user-friendly information such as links to Jenkins builds, source references, etc.
    ```bash
    function info_hook()
    {
        # your hook implementation
        :
    }
    ```
  - `drs-put-hook.sh` is called by the `put` command before commit. It can be used to collect all necessary information about a revision (a build). This can be used by the `info` command, for example.
    ```bash
    function put_hook()
    {
       # your hook implementation
       :
    }
    ```

#### Jenkins example

Given you have a Jenkins job which is producing your builds. `drs-put-hook.sh` will dump `env` to a file `env.json`. Then it will be committed and pushed to the metadata repository.

`drs-put-hook.sh`:

```bash
function put_hook()
{
  jq -n env > env.json
}
```

Clients consuming these builds will use `info` and can get valuable information.

`drs-info-hook.sh`:

```bash
function info_hook()
{
  change_branch=$(jq -r '.CHANGE_BRANCH' env.json)
  if [[ "${change_branch}" != "null" ]]; then
    branch="${change_branch}"
    pr="true"
  else
    branch=$(jq -r '.BRANCH_NAME' env.json)
  fi

  echo "branch: ${branch}"
  if [[ -n "${pr}" ]]; then
    echo "PR: $(jq -r '.BRANCH_NAME' env.json)"
    echo "PR link: $(jq -r '.CHANGE_URL' env.json)"
  fi

  build_url=$(jq -r '.BUILD_URL' env.json)
  echo "build link: ${build_url}"

  job_url=$(jq -r '.JOB_URL' env.json)
  echo "job link: ${job_url}"
}
```

:memo: Jenkins adds many environment variables to builds implicitly. The actual availability depends on your job setup.

### Putting your initial directory revision

1.  Make sure you pushed your configuration files `drs.json` and `.gitignore`
2.  Copy your initial content to the working directory
3.  Put your directory to remote:
    ```bash
    git drs-put
    ```

## Usage

### A simple example

#### Producer

```bash
# create a new branch (based on the source branch)
git drs-create myFeature
# put new build artifacts to remote
git drs-put
```

#### Consumer

```bash
# select the branch you need a build from
git drs-select myFeature
# update to the latest available build
git drs-update
# get the build
git drs-get
```

### Command reference

Command syntax is the following:

```bash
git drs-<command> [options] [arguments]
```

Optional elements are shown in brackets [ ]. For example, many commands take a branch name as an argument.

To get some information about a command and a link to its reference documentation, use `command` with `help`:

```bash
git drs-<command> help
```

:bulb: You can also use commands without a *Git* alias; this is recommended for scripts. Refer to the command name when calling:

```bash
$DRS_HOME/<command>.sh
```

-----

#### info

The commit message is not very informative. To get more user-friendly information, use `info`:

```bash
git drs-info
```

The `info` command implementation is project-specific; see section [Hooks](#hooks).

-----

#### name

To get the current branch name, use `name`:

```bash
git drs-name
```

-----

#### select

To select and switch to an existing branch, use `select`:

```bash
git drs-select [<branch>|<tag>|<uuid>]
```

Arguments:

  - `branch, tag` - the branch or tag to select; if not specified, the `defaultBranch` property will be used (optional)
  - `uuid` - the uuid to select; alternatively, this searches the log for a specific uuid (optional)

:memo: `uuid` based selection is useful to identify builds; for example, Jenkins can post the `uuid` for each build and users can use this directly.

![jenkins uuid](docs/jenkins-uuid.png)
-----

#### usage

To show remote storage consumption, use `usage`:

```bash
git drs-usage [-v,--verbose]
```

Options:

  - `verbose` - show the detailed breakdown table of all remote revision directories (optional)

It connects to the remote host via SSH and queries the total storage size of the configured remote path. If `--verbose` is provided, it also lists all directory revisions (UUIDs) along with their sizes, creation dates, git commit hashes, branches/tags, and sequence messages.

:warning: **Note on Active vs. Orphaned accuracy:**
Classification of a remote revision as active or orphaned depends entirely on the state of your local Git repository history. If your local clone is stale (e.g. requires `git fetch`), shallow (e.g. `git clone --depth 1`), or missing branches (e.g. `--single-branch`), some active commits will not be visible locally. Consequently, active remote revisions might be reported as `[orphaned]`. Ensure your local clone is up-to-date and complete for accurate reporting.

-----

#### update

To get to the latest revision, use `update`:

```bash
git drs-update
```

:memo: If you are in a detached HEAD state (not on any branch), `update` will fail. You need to select a branch then update it.

-----

#### get

To get the directory revision specified by the current commit. The working directory content will be synchronized with this revision.

```bash
git drs-get [-v,--verbose] [-q,--quiet] [--stats] [--latest] [<target_directory>]
```

Options:

  - `verbose` - sets *rsync* verbose mode (optional)
  - `quiet` - sets *rsync* quiet mode (optional)
  - `stats` - enables *rsync* statistics (optional)
  - `latest` - combines `update` and `get` to get the latest version

Arguments:

  - `target_directory` – the directory to get content to; if not specified, the `name` property will be used (optional)

:bulb: Usually you are only interested in the latest version; this can be done with a one-liner:

```bash
git drs-get --latest
```

-----

#### create

To create a new branch, use `create`:

```bash
git drs-create [<branch>]
```

Arguments:

  - `branch` - the branch to create (mandatory)

-----

#### put

To put a revision to the remote host, use `put`:

```bash
git drs-put [-v,--verbose] [--no-sequence-check] [-s,--sequence <sequence_number>] [<source_directory>]
```

Options:

  - `verbose` - sets *rsync* verbose mode (optional)
  - `quiet` - sets *rsync* quiet mode (optional)
  - `stats` - enables *rsync* statistics (optional)
  - `no-sequence-check` - disables sequence number checking
  - `sequence_number` - the sequence number; must be a comparable decimal (optional)

Arguments:

  - `source_directory` – the directory to put content from (optional)

Simple Jenkins example for using `--sequence`:

```bash
$DRS_HOME/create.sh $BRANCH_NAME
$DRS_HOME/update.sh
$DRS_HOME/put.sh --sequence $BUILD_ID my_build_dir
```

:memo: `BRANCH_NAME` and `BUILD_ID` are Jenkins job variables.

`source_directory` allows you to use a source directory, eliminating the need to stage (copy) content to the working directory.

## Differences to Git

Since **drs** uses *Git* more like a database, not all *Git* concepts apply. Especially collaboration is completely different in a **drs** metadata repository.

:warning: In case you want to work with *native* *Git* commands, the following notes are important to understand:

  - **Origin has precedence**

    To keep the workflow simple and robust, origin has precedence. Commands will force you to be up-to-date with origin and `drs-put` will implicitly try to push the new revision. This ensures whatever happens, users will fall back to a public *last known* version. Origin is the single source of truth, which is much less error-prone in a single-producer, multiple-consumer context.

  - **No merging**

    Revisions are not stored in *Git*; they are simple directories somewhere. As you cannot merge a directory on a filesystem, you cannot merge in **drs** either.

  - **Commit message format**

    The commit message has a strict format. You should not create them manually.

:memo: **No merging** implies that branches are not merged. They are created then deleted if not needed. It's possible to keep all branches if you want to keep all history.

## Retention

Deleting revisions is done by deleting directories on the remote host. **drs** will try to locate a revision; if not found, it's assumed to be deleted. This is part of the normal workflow and will not be treated as an error.

To automate storage management, a server-side cleanup utility is provided in [src/cleanup.sh](file:///home/bvarnai/work/drs/src/cleanup.sh). It parses the metadata repository (the Git repo) as the source of truth to determine which revision directories are obsolete and can be deleted.

### Cleanup Rules
The script operates on the following rules:
1. **Open Branches**: Retains the last $N$ commits (default: 5) and any commit not older than $M$ days (default: 30) for all active local branches (`refs/heads/*`).
2. **Deleted Branches**: If a branch is deleted, its related revision artifacts are automatically purged unless kept by another active branch or tag.
3. **Tags**: Never deletes revision artifacts associated with any Git tag.

### Usage
Run the cleanup script directly on the remote server:
```bash
./cleanup.sh [options] <git-dir> <storage-dir>
```

Options:
* `-n`, `--dry-run`: Show what would be deleted without making any changes.
* `-d`, `--days <N>`: Number of days of commits to keep on open branches (default: 30).
* `-c`, `--commits <N>`: Number of latest commits per open branch to keep (default: 5).
* `-h`, `--help`: Show the help message.

### Cron Integration & Remote Repositories (e.g., GitHub)

If your metadata repository is hosted on a remote platform like GitHub, your cleanup server needs to sync the latest branch, tag, and commit metadata before running the cleanup.

#### 1. Initial Setup
Create a **bare clone** of your GitHub repository on the storage server under the `drs` user:
```bash
git clone --bare git@github.com:username/your-drs-metadata-repo.git /home/drs/drs-metadata.git
```

#### 2. Automating with Cron
Add a cron job that fetches the latest updates from GitHub (pruning deleted branches and updating tags) and then runs the cleanup script:
```cron
0 2 * * * git --git-dir=/home/drs/drs-metadata.git fetch origin --prune --tags && /home/drs/cleanup.sh --days 30 --commits 5 /home/drs/drs-metadata.git /home/drs/drs-home/myproject >> /home/drs/drs-cleanup.log 2>&1
```

* **Note**: Make sure that the SSH keys for the `drs` user on the storage server are configured with read access to the GitHub repository so `git fetch` can run non-interactively without prompting for credentials.

## Development notes

*Git* was a convenient choice to make something distributed and transactional. Directory metadata is published as a *Git* commit message in `json` format. :cold\_sweat: ugh, you might say, and you are probably right. I abused the commit message, but in a good way, embracing the tremendous flexibility *Git* offers. I didn't use *Git* notes because I don't have anything to annotate; I just want to record something.

So a typical **drs** commit message looks like this:

```json
{"uuid":"c1ca82b1-7f34-4f4c-9a76-05e3297b2a23","seq":"1622824489"}
```

The `uuid` is used to identify the directory on the remote host. The sequence number helps to drop outdated builds.

*rsync* is a great tool when you have small deltas to deal with. Initially, I wanted to use a "trendy" S3 ([minIO](https://min.io/) for example) based solution, but I realized not much is gained there. I think for a small development team, these just add unnecessary overhead.

### Shell vs. python, groovy etc.

Obviously, this is a very subjective topic. I wanted to rely on external tools and keep it as simple as possible. No advanced logic and the seamless integration with *Git* aliases pushed me in the direction to use shell only.

I used Google's [Shell Style Guide](https://google.github.io/styleguide/shellguide.html) with the help of [ShellCheck](https://www.shellcheck.net/).

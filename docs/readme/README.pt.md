# nexus-trade-bot

<p align="center">
  <img src="../../logo/logo.png" alt="Nexus Trade Bot" width="720">
</p>

**Um centro de controle de bot de grade criado para traders que desejam volume, automação e visibilidade de risco sem cuidar de cada pedido. Futuros é o modo padrão; grades spot são suportadas nas principais bolsas centralizadas.**

[![Go](https://img.shields.io/badge/Go-1.26%2B-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![License](https://img.shields.io/badge/license-GPL--3.0-green)](../../LICENSE)
[![One Command](https://img.shields.io/badge/install-one%20command-blue)](#instalação-com-um-comando)
[![Languages](https://img.shields.io/badge/languages-11-orange)](#idiomas)

## Entre na comunidade

Dúvidas de implantação, detalhes das APIs das exchanges, experiência de trading real e feedback de novas versões são resolvidos melhor em um só lugar. Entre no grupo de usuários do Nexus Trade Bot: [https://t.me/nexustradebot8](https://t.me/nexustradebot8).

## Idiomas

[English](../../README.md) | [简体中文](README.zh-CN.md) | [Русский](README.ru.md) | [한국어](README.ko.md) | [日本語](README.ja.md) | [Español](README.es.md) | [Tiếng Việt](README.vi.md) | [हिन्दी](README.hi.md) | Português | [العربية](README.ar.md) | [繁體中文](README.zh-TW.md)

## Leia isto primeiro

Se você é usuário final, comece pela instalação com um comando, abra o console web, adicione uma API com permissão de trading e sem permissão de saque, e teste primeiro com um bot pequeno.

Se você é desenvolvedor, comece compilando a partir do código fonte, revise `config.example.yaml`, rode `go test ./...` e use worker mode quando quiser iniciar um bot com um arquivo de configuração específico.

O README segue do uso prático ao detalhe técnico: instalação, exchanges, recursos, exemplos de estratégia, parâmetros, instalação manual e checklist antes de operar ao vivo.

## Instalação com um comando

Execute isso em um novo servidor Ubuntu:

```bash
wget -O nexus-trade-bot.sh https://raw.githubusercontent.com/haohaoi34/nexus-trade-bot/main/scripts/nexus-trade-bot.sh && chmod +x nexus-trade-bot.sh && ./nexus-trade-bot.sh install && ./nexus-trade-bot.sh start
```

O executor do servidor automaticamente:

- Instala dependências ausentes do Ubuntu.
- Instala o Go se o servidor não tiver uma versão compatível.
- Clona `https://github.com/haohaoi34/nexus-trade-bot.git` quando ele ainda não está em execução dentro de um checkout de origem.
- Constrói o bot a partir do código-fonte ou usa o binário incluído em um pacote de lançamento.
- Cria `config.yaml` a partir de `config.example.yaml` se necessário e o mantém local.
- Inicia o console da web em segundo plano e grava logs em `logs/`.
- Mostra um bloco de acesso claro com URL local, endereço de bind, arquivo PID e caminhos de log. O acesso remoto fica desativado até você definir um bind público explicitamente.

Comandos de servidor úteis:

```bash
./nexus-trade-bot.sh install
./nexus-trade-bot.sh start
./nexus-trade-bot.sh status
./nexus-trade-bot.sh logs
./nexus-trade-bot.sh restart
./nexus-trade-bot.sh stop
./nexus-trade-bot.sh update
```

Login da web padrão:

```text
username: admin
password: admin
```

Altere a senha padrão imediatamente após seu primeiro login.

## Exchanges suportadas

- Binance ☑️
- Bitget ☑️
- Gate.io ☑️
- Bybit ☑️
- OKX ☑️
- Hyperliquid ☑️



## O que faz

nexus-trade-bot ajuda você a executar estratégias de grade a partir de um console web limpo:

- Adicione APIs de troca uma vez e verifique-as antes de usar.
- Crie vários bots para diferentes símbolos, contas e direções.
- Escolha futuros ou spot. Futuros é selecionado por padrão.
- Use o modo comprado, vendido ou neutro em futuros; use o modo longo no local.
- Carregue símbolos spot Binance, Bitget, Bybit, OKX, Gate e Hyperliquid automaticamente.
- Assista saldos, volume de negociação, status do bot e PnL em tempo real.
- Pause um bot, altere os parâmetros e reinicie-o com as configurações mais recentes.
- Deixe o monitor de risco parar de negociar durante movimentos anormais do mercado.

Ele foi projetado para traders que se preocupam com execução, rotatividade e controle, não para pessoas que desejam editar arquivos de configuração o dia todo.

## A ideia central

Um grid bot coloca ordens de compra e venda em intervalos de preços fixos. Em vez de tentar prever o topo ou o fundo exato, ele continua trabalhando em torno de uma faixa de preço:

- Quando o preço cai, o bot compra gradualmente de acordo com as configurações da sua grade.
- Quando o preço sobe, o bot vende níveis mais altos passo a passo.
- Num mercado em recuperação lateral ou ascendente, isto pode transformar a volatilidade em repetidas negociações realizadas.
- Numa tendência de baixa unilateral, o bot acumula posição e precisa de margem suficiente, limites de risco e paciência.

O objetivo não é o lucro mágico. O objetivo é a execução disciplinada: espaçamento consistente de ordens, tamanho de ordem controlado, risco visível e reação automática quando o mercado se torna anormal.


## Exemplo de estratégia: grade ETH com alta rotatividade

Aqui está um exemplo prático para entender como os traders usam esse tipo de bot.

Suponha que a ETH esteja sendo negociada perto de `3000` e você configure:

| Parâmetro | Exemplo |
| --- | --- |
| Símbolo | `ETHUSDT` ou `ETHUSDC` |
| Direção | Grade longa |
| Intervalo de preço | `1 USDT` |
| Valor do pedido | `300 USDT` por ordem de grade |
| Estilo de mercado | Mercado em recuperação lateral ou ascendente |

Com um intervalo `1 USDT` apertado e liquidez ETH ativa, o bot pode gerar um giro muito alto. Num mercado movimentado, este tipo de configuração pode atingir milhões de dólares em volume diário de negociação e dezenas de milhões em volume mensal, dependendo da volatilidade, taxas, liquidez e tamanho da conta.

É por isso que muitos comerciantes usam sistemas de rede para dois propósitos:

- **Aumento de volume**: aumento do volume de negociação de futuros para níveis ou campanhas VIP de exchanges.
- **Colheita de volatilidade**: comprar repetidamente em baixa e vender em alta dentro de uma faixa.


## Exemplo de lógica de rebaixamento

A negociação em grade deve ser planejada em torno do rebaixamento.

Suponha que a ETH comece perto de `3000` e caia para `2700`. Uma grade longa geralmente terá uma perda flutuante porque comprou ao longo do caminho de queda. Mas também acumulou entradas mais baixas. Se o preço posteriormente se recuperar de `2700` em direção a `2850`, o custo médio poderá ser reduzido o suficiente para que a conta se aproxime do ponto de equilíbrio antes de uma única entrada em `3000`.

Se o ETH retornar próximo à área `3000` original, a estratégia poderá se beneficiar de ambos:

- recuperação de estoque da recuperação;
- spreads de grade realizados coletados durante o movimento.

Alguns traders reservam um buffer de margem maior, por exemplo, em torno de `30,000 USDT`, para projetar uma grade que possa tolerar um movimento muito mais profundo, como uma redução de ETH `1000 USDT`. Se isso é suficiente depende da alavancagem, modo de margem, tamanho da posição, taxas, regras de margem de manutenção cambial e quão agressiva é sua grade.

O ponto importante: o lucro da rede vem da preparação e não do otimismo. Antes de calcular o tamanho, calcule até onde o mercado pode se mover contra você, quanta posição o bot pode acumular e o que acontece se o mercado não se recuperar rapidamente.


## Proteção integrada contra riscos

Quedas rápidas unidirecionais são o pior ambiente para uma rede longa e agressiva. nexus-trade-bot inclui um monitor de risco de mercado projetado para reduzir este problema:

- observa símbolos importantes como BTC, ETH, SOL, XRP e DOGE;
- detecta comportamento anormal de preços e volumes;
- interrompe a negociação quando as condições de mercado se tornam perigosas;
- permite a negociação novamente somente após a recuperação de símbolos monitorados suficientes.

Isso não elimina o risco, mas dá ao bot a chance de parar de adicionar exposição durante movimentos repentinos de liquidação.


## Maneiras comuns de usá-lo

### 1. Volume e construção de nível VIP

Use intervalos apertados e tamanho de pedido controlado em símbolos de grande liquidez. O objetivo é alta rotatividade com execução previsível. As taxas são muito importantes aqui, então use pares de taxas baixas ou programas de descontos sempre que possível.

### 2. Grade longa após uma retração do mercado

Comece após uma queda significativa em vez de perseguir uma bomba vertical. O bot compra em camadas e vende em rebotes. Este estilo precisa de margem suficiente para sobreviver a retrocessos mais profundos.

### 3. Grade de pontos da Binance

Use o modo spot quando quiser que o bot compre e venda moedas reais em vez de abrir posições futuras alavancadas. O modo Spot é apenas longo: o bot compra primeiro os níveis mais baixos e vende o estoque em rebotes. É mais simples que os futuros, mas ainda precisa de equilíbrio de cotações suficiente e de um plano para tendências de baixa prolongadas.

### 4. Saída de estoque

Se você já possui uma posição, o bot pode ajudar a vendê-la gradualmente à medida que o preço aumenta. Quando a posição estiver totalmente reduzida, você poderá parar o bot.

### 5. Grade Neutra

Use o modo neutro quando desejar o comportamento da grade no lado longo e no lado curto. Comece com um tamanho menor e observe como a bolsa lida com o modo de posição antes de dimensionar.


### 6. Grid clássico

O grid clássico funciona apenas em futuros e usa modo neutro. Ele mantém 50 ordens de compra ativas abaixo do preço de grid atual e 50 ordens de venda ativas acima, sem faixa superior ou inferior. O alvo é 100 ordens ativas no total, com reposição automática após execuções. Hyperliquid futures ainda não é compatível porque o modo exige comportamento neutro de hedge.

## Guia de parâmetros

| Configuração | O que isso significa | Dica Prática |
| --- | --- | --- |
| `symbol` | Par de negociação | Comece com pares líquidos como BTC ou ETH. |
| `app.market_type` | `futures` ou `spot` | O padrão é `futures`. A negociação spot ao vivo oferece suporte a Binance, Bitget, Bybit, OKX, Gate e Hyperliquid por meio de adaptadores dedicados. |
| `mode` | `normal`, `aggressive` ou `classic` | `classic` força futures + neutral e mira 100 ordens ativas: 50 compras e 50 vendas. |
| `direction` | `long`, `short` ou `neutral` | Grades longas precisam de margem para rebaixamentos. As grades curtas não devem adotar acidentalmente uma posição curta manual não relacionada, a menos que você ative esse comportamento intencionalmente. |
| `price_interval` | Distância entre níveis de grelha | Intervalo menor significa mais negociações e mais taxas. |
| `order_quantity` | Montante utilizado por encomenda | Quantidade maior aumenta o giro e o rebaixamento. Confirme se a IU está mostrando o valor da cotação ou a quantidade base para sua bolsa e tipo de mercado. |
| `min_order_value` | Pedido mínimo nocional | Deve satisfazer os mínimos de troca. |
| `risk_control.enabled` | Proteção contra anomalias de mercado | Mantenha-o ativado, a menos que você saiba exatamente por que não. |

##Console Web

O console oferece suporte a 11 idiomas:

Inglês, Chinês Simplificado, Russo, Coreano, Japonês, Espanhol, Vietnamita, Hindi, Português, Árabe e Chinês Tradicional.

O modo Console da Web mostra:

- Gerenciamento de APIs
- criação e edição de bots
- trocar logotipos
- saldos em tempo real
- hoje e PnL total realizado
- hoje e volume total de negociação
- estados de bot em execução, pausado e interrompido


## Instalação manual

```bash
git clone https://github.com/haohaoi34/nexus-trade-bot.git
cd nexus-trade-bot
go mod download
go build -o nexus-trade-bot .
```

Inicie o console da Web:

```bash
./nexus-trade-bot
```

URL local padrão:

```text
http://127.0.0.1:8080
```

Expor em um servidor:

```bash
NEXUS_TRADE_BOT_ADDR=0.0.0.0:8080 ./nexus-trade-bot
```

Executor de servidor de um comando a partir de uma verificação de origem:

```bash
chmod +x scripts/nexus-trade-bot.sh
scripts/nexus-trade-bot.sh install
scripts/nexus-trade-bot.sh start
scripts/nexus-trade-bot.sh status
scripts/nexus-trade-bot.sh logs
scripts/nexus-trade-bot.sh stop
```

O executor funciona tanto a partir de um checkout de origem quanto de um pacote de lançamento. No modo fonte ele constrói `./nexus-trade-bot`; no modo de lançamento, ele usa o binário incluído diretamente.

Execute o modo de trabalho CLI:

```bash
./nexus-trade-bot worker config.yaml
```


## Antes de negociar ao vivo

Verifique primeiro:

- A chave API tem permissão de negociação, mas não tem permissão de retirada.
- O modo de margem é o que você espera.
- A alavancagem não é muito agressiva.
- O símbolo tem liquidez suficiente.
- O tamanho do pedido atende aos mínimos de troca.
- Você entende quanta posição a grade pode acumular.
- Você tem um plano para mercados unidirecionais.
- O firewall do servidor expõe a porta da web somente quando pretendido.


## Isenção de responsabilidade

A negociação de futuros pode causar perdas significativas. As estratégias de grade podem ter um bom desempenho em mercados limitados ou em recuperação, mas também podem acumular grandes posições durante fortes tendências unidirecionais. nexus-trade-bot é um software de execução; você é responsável pelas configurações da estratégia, configuração da exchange, risco da conta e todas as negociações realizadas por meio de suas chaves API.

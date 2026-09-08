# Registo de prestadores e destinatários externos

Estado: modelo de governação aprovado em `2026-09-08`; prova contratual e configuração real pendentes. Este documento não confirma termos de conta, regiões, subprocessadores ou transferências.

## Regra de manutenção

Antes da ativação e depois anualmente, bem como antes de qualquer novo prestador, subprocessador, região ou serviço material, registar e aprovar: papel jurídico, finalidade, categorias, titulares, serviço/produto, região de dados e acesso, contrato/DPA, subprocessadores, mecanismo e avaliação de transferência, retenção configurada/contratual, canal de eliminação, prova verificada, responsável e data da próxima revisão. Preferir tratamento no EEE; qualquer acesso fora do EEE exige mecanismo e avaliação documentados.

## Inventário técnico conhecido

| Entidade | Papel a confirmar | Utilização conhecida no repositório | Dados possíveis | Evidência ainda necessária |
|---|---|---|---|---|
| Hetzner | Subcontratante | Servidor, aplicação, PostgreSQL e backups do servidor | Dados alojados, metadados técnicos e cópias | Conta/contrato/DPA, localização efetiva, subprocessadores, acessos, retenção e eliminação |
| Amazon Web Services | Subcontratante por serviço | S3/KMS, backups, SES, CloudWatch e configuração operacional; Route 53/ECR sem dados de membro por desenho | Objetos, cópias cifradas, endereço/mensagem de email e registos técnicos | Conta, serviços ativos, regiões, DPA, subprocessadores, transferências, retenção e rotas de eliminação |
| Cloudflare | Papel por serviço a confirmar | Tunnel/DNS/proteção de rede e Turnstile | IP, sinais técnicos, token de desafio e metadados de rede | Conta, produtos/configuração, região/acessos, DPA, subprocessadores, transferências e retenção |
| GitHub | Subcontratante apenas quando aplicável | CI e artefactos de deployment | Código e dados sintéticos; dados pessoais de produção são proibidos | Organização/contrato, região/acessos, subprocessadores e confirmação das retenções configuradas |
| FPC, organizadores, seguradora e autoridades | Normalmente responsável autónomo; confirmar por fluxo | Inscrição, resultados, incidentes, seguro ou obrigação concreta | Apenas campos necessários ao fluxo documentado | Identidade da entidade, fundamento, campos, prazo, canal de direitos/notificação e resposta |

SMTP de produção está desenhado para AWS SES. Qualquer substituição constitui alteração material e exige atualização prévia deste registo e da matriz.

## Eliminação e prova

- Configurar a menor retenção suportada compatível com a matriz aprovada.
- #111 executa API, lifecycle, revogação ou pedido contratual quando disponível, com repetição limitada e logs opacos.
- Uma entidade já desligada ou um objeto já ausente conta como sucesso idempotente.
- Uma mensagem já entregue ou um registo sob controlo de responsável autónomo pode não ser eliminável pelo CFC; documentar o limite e informar quando aplicável, sem deixar o caso indefinidamente pendente.
- Guardar apenas referência, ação, resultado, data e prova operacional sem copiar o dado apagado.
